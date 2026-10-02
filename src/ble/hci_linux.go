//go:build linux

package ble

import (
	"context"
	"encoding/binary"
	"fmt"
	"syscall"
	"time"
	"unsafe"
)

// Direct HCI scanner (bluetooth.mode: hci), 2.10.2026.
//
// Why: BlueZ D-Bus discovery stops and restarts LE scanning every 10.24 s (kernel DISCOV_LE_TIMEOUT, bluetoothd
// re-arms it): measured with btmon 24 × LE Set Scan Enable per 2 min, ~8600 per day. The Broadcom BCM20702 in the
// ASUS USB-BT400 hangs on that command roughly once per 200–300 toggles ("command 0x200c tx timeout",
// "Unable to disable scanning"), about every 35 min, each hang a 2+ min gap in data. The same software on a
// Realtek RTL8761B (USB-BT500) had zero errors in 7 days, so the trigger is the toggling on this chip.
//
// This mode takes the controller exclusively (HCI_CHANNEL_USER, bluetoothd stops managing it), resets it once,
// enables passive scanning once and never stops it. Duplicate filtering is off, so every advertisement arrives.
// A silent controller (no Ruuvi report for hciSilenceTimeout) ends the session; Start() then resets it again.
// Requires CAP_NET_ADMIN and the host network namespace (AF_BLUETOOTH exists only there).

const (
	afBluetooth       = 31
	btprotoHCI        = 1
	hciChannelUser    = 1
	hciDevDown        = 0x400448CA // _IOW('H', 202, int)
	hciCommandPkt     = 0x01
	hciCmdTimeout     = 2 * time.Second
	hciReadTimeout    = 1 * time.Second
	hciSilenceTimeout = 60 * time.Second
)

// hciStep is one initialisation command.
type hciStep struct {
	op uint16
	p  []byte
}

type hciSockaddr struct {
	family  uint16
	dev     uint16
	channel uint16
}

// openUserChannel brings the adapter down (bluetoothd may hold it up) and binds it exclusively.
func openUserChannel(dev int) (int, error) {
	fd, err := syscall.Socket(afBluetooth, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, btprotoHCI)
	if err != nil {
		return -1, fmt.Errorf("socket(AF_BLUETOOTH): %w", err)
	}
	var lastErr error
	for i := 0; i < 5; i++ { // bluetoothd can power the adapter back up between down and bind
		syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), hciDevDown, uintptr(dev))
		sa := hciSockaddr{family: afBluetooth, dev: uint16(dev), channel: hciChannelUser}
		_, _, e := syscall.Syscall(syscall.SYS_BIND, uintptr(fd), uintptr(unsafe.Pointer(&sa)), unsafe.Sizeof(sa))
		if e == 0 {
			tv := syscall.NsecToTimeval(int64(hciReadTimeout))
			syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)
			return fd, nil
		}
		lastErr = e
		time.Sleep(300 * time.Millisecond)
	}
	syscall.Close(fd)
	return -1, fmt.Errorf("bind hci%d user channel: %w", dev, lastErr)
}

// hciCmd sends a command and waits for its Command Complete / Command Status (other events are skipped).
// Returns the Command Complete return parameters after the status byte.
func hciCmd(fd int, opcode uint16, params ...byte) ([]byte, error) {
	pkt := append([]byte{hciCommandPkt, byte(opcode), byte(opcode >> 8), byte(len(params))}, params...)
	if _, err := syscall.Write(fd, pkt); err != nil {
		return nil, fmt.Errorf("write 0x%04x: %w", opcode, err)
	}
	buf := make([]byte, 260)
	deadline := time.Now().Add(hciCmdTimeout)
	for time.Now().Before(deadline) {
		n, err := syscall.Read(fd, buf)
		if err != nil {
			if err == syscall.EAGAIN || err == syscall.EINTR {
				continue
			}
			return nil, fmt.Errorf("read 0x%04x: %w", opcode, err)
		}
		if n < 3 || buf[0] != hciEventPkt {
			continue
		}
		if st, ret, ok := cmdResult(buf[1:n], opcode); ok {
			if st != 0 {
				return nil, fmt.Errorf("command 0x%04x status 0x%02x", opcode, st)
			}
			return append([]byte(nil), ret...), nil
		}
	}
	return nil, fmt.Errorf("command 0x%04x timeout", opcode)
}

// runHCISession initialises the controller, enables passive scanning once and delivers Ruuvi advertisements
// until the context ends, the device disappears (USB reset) or nothing has been heard for hciSilenceTimeout.
func (s *Scanner) runHCISession(ctx context.Context) error {
	dev := s.cfg.Bluetooth.HCIDevice
	fd, err := openUserChannel(dev)
	if err != nil {
		return err
	}
	defer syscall.Close(fd) // closing the user channel closes the device (scan stops with it)

	interval := scanUnits(float64(s.cfg.Bluetooth.ScanInterval)/float64(time.Millisecond), 0x0010)
	window := scanUnits(float64(s.cfg.Bluetooth.ScanWindow)/float64(time.Millisecond), 0x0010)
	if window > interval {
		window = interval
	}
	evMask := make([]byte, 8)
	binary.LittleEndian.PutUint64(evMask, 0x3DBFF807FFFBFFFF) // kernel default, includes LE Meta (bit 61)
	for _, st := range []hciStep{{opReset, nil}, {opSetEventMask, evMask}} {
		if _, err := hciCmd(fd, st.op, st.p...); err != nil {
			return fmt.Errorf("hci%d init: %w", dev, err)
		}
	}
	// BLE 5 extended advertising (bit 12) → extended scan, otherwise legacy. A controller that supports it delivers
	// extended-only advertisements (Ruuvi Air format E1) only to extended scanning; mixing legacy and extended
	// scan commands is not allowed. Coded PHY (bit 11) is scanned too, like the kernel does.
	feat, err := hciCmd(fd, opLEReadFeatures)
	if err != nil {
		return fmt.Errorf("hci%d init: %w", dev, err)
	}
	ext := len(feat) >= 2 && feat[1]&0x10 != 0
	coded := ext && feat[1]&0x08 != 0
	leMask := []byte{0x1F, 0, 0, 0, 0, 0, 0, 0} // includes LE Advertising Report
	if ext {
		leMask[1] = 0x10 // LE Extended Advertising Report
	}
	steps := []hciStep{{opLESetEventMask, leMask}}
	if ext {
		phys, per := byte(0x01), []byte{0x00, byte(interval), byte(interval >> 8), byte(window), byte(window >> 8)} // passive
		params := append([]byte{0x00, 0x00, 0}, per...)                                                             // own addr public, accept all
		if coded {
			phys |= 0x04
			params = append(params, per...)
		}
		params[2] = phys
		steps = append(steps, hciStep{opLESetExtScanPar, params}, hciStep{opLESetExtScanEn, []byte{0x01, 0x00, 0, 0, 0, 0}}) // enable once, no duplicate filter, no duration
	} else {
		steps = append(steps, hciStep{opLESetScanParam, []byte{0x00, byte(interval), byte(interval >> 8), byte(window), byte(window >> 8), 0x00, 0x00}}, hciStep{opLESetScanEnable, []byte{0x01, 0x00}}) // enable once, duplicate filtering off
	}
	for _, st := range steps {
		if _, err := hciCmd(fd, st.op, st.p...); err != nil {
			return fmt.Errorf("hci%d init: %w", dev, err)
		}
	}
	s.logger.Info("BLE scan started (direct HCI, passive, continuous)", "hci_device", dev, "extended", ext, "coded_phy", coded,
		"interval_ms", float64(interval)*0.625, "window_ms", float64(window)*0.625)

	buf := make([]byte, 260)
	frag := fragments{}
	last := time.Now()
	for {
		if ctx.Err() != nil {
			return nil
		}
		n, err := syscall.Read(fd, buf)
		if err != nil {
			if err == syscall.EAGAIN || err == syscall.EINTR {
				if time.Since(last) > hciSilenceTimeout {
					return fmt.Errorf("no Ruuvi advertisements for %v", hciSilenceTimeout)
				}
				continue
			}
			return fmt.Errorf("hci%d read: %w", dev, err) // ENODEV after USB reset → session restart
		}
		if n < 3 || buf[0] != hciEventPkt {
			continue
		}
		reports, perr := parseAdvReports(buf[1:n])
		if perr != nil {
			s.logger.Debug("Malformed advertising report", "error", perr)
		}
		for _, r := range reports {
			data, done := frag.add(r) // extended data may come in several reports
			if !done {
				continue
			}
			if payload := manufacturerData(data, RuuviManufacturerID); len(payload) > 0 {
				last = time.Now()
				s.logger.Trace("Ruuvi advertisement received", "mac", r.addr, "rssi", r.rssi, "format", payload[0], "data_len", len(payload))
				s.handler(r.addr, r.rssi, append([]byte(nil), payload...)) // buf is reused
			}
		}
		if time.Since(last) > hciSilenceTimeout {
			return fmt.Errorf("no Ruuvi advertisements for %v", hciSilenceTimeout)
		}
	}
}
