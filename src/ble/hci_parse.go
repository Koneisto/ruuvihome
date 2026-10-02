package ble

import "fmt"

// HCI packet parsing for the direct (user channel) scanner, see hci_linux.go.
// Kept OS-independent so it can be unit tested anywhere.

const (
	hciEventPkt        = 0x04
	hciEvCmdComplete   = 0x0E
	hciEvCmdStatus     = 0x0F
	hciEvLEMeta        = 0x3E
	hciSubevAdvReport  = 0x02
	hciSubevExtAdv     = 0x0D // LE Extended Advertising Report (BLE 5, e.g. Ruuvi Air format E1)
	adTypeManufacturer = 0xFF

	opReset           = 0x0C03
	opSetEventMask    = 0x0C01
	opLESetEventMask  = 0x2001
	opLESetScanParam  = 0x200B
	opLESetScanEnable = 0x200C
	opLEReadFeatures  = 0x2003
	opLESetExtScanPar = 0x2041
	opLESetExtScanEn  = 0x2042
)

// advReport is one legacy LE Advertising Report.
type advReport struct {
	addr   string // "C8:8A:AB:3D:7B:E8"
	rssi   int
	data   []byte // AD structures
	sid    byte   // extended: advertising set id
	status byte   // extended: 0 complete, 1 incomplete (more follows), 2 truncated
}

// parseAdvReports decodes an LE Meta / LE Advertising Report event (packet without the 0x04 type byte:
// [event code, param len, subevent, num reports, reports...]). Reports are laid out one after another
// (event type, address type, address, data length, data, RSSI), as the Linux kernel parses them.
func parseAdvReports(ev []byte) ([]advReport, error) {
	if len(ev) >= 4 && ev[0] == hciEvLEMeta && ev[2] == hciSubevExtAdv {
		return parseExtAdvReports(ev)
	}
	if len(ev) < 4 || ev[0] != hciEvLEMeta || ev[2] != hciSubevAdvReport {
		return nil, nil
	}
	n, p := int(ev[3]), 4
	out := make([]advReport, 0, n)
	for i := 0; i < n; i++ {
		if p+9 > len(ev) {
			return out, fmt.Errorf("truncated report %d", i)
		}
		a := ev[p+2 : p+8]
		l := int(ev[p+8])
		if p+9+l+1 > len(ev) {
			return out, fmt.Errorf("truncated data in report %d", i)
		}
		out = append(out, advReport{
			addr: fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", a[5], a[4], a[3], a[2], a[1], a[0]),
			data: ev[p+9 : p+9+l],
			rssi: int(int8(ev[p+9+l])),
		})
		p += 9 + l + 1
	}
	return out, nil
}

// parseExtAdvReports decodes LE Extended Advertising Report: per report event type (2), address type, address (6),
// primary PHY, secondary PHY, SID, TX power, RSSI, periodic interval (2), direct address type, direct address (6),
// data length, data — 24 bytes before the data. Data status is bits 5–6 of the event type.
func parseExtAdvReports(ev []byte) ([]advReport, error) {
	n, p := int(ev[3]), 4
	out := make([]advReport, 0, n)
	for i := 0; i < n; i++ {
		if p+24 > len(ev) {
			return out, fmt.Errorf("truncated extended report %d", i)
		}
		et := uint16(ev[p]) | uint16(ev[p+1])<<8
		a := ev[p+3 : p+9]
		l := int(ev[p+23])
		if p+24+l > len(ev) {
			return out, fmt.Errorf("truncated extended data in report %d", i)
		}
		out = append(out, advReport{
			addr:   fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", a[5], a[4], a[3], a[2], a[1], a[0]),
			rssi:   int(int8(ev[p+13])),
			sid:    ev[p+11],
			status: byte(et>>5) & 0x03,
			data:   ev[p+24 : p+24+l],
		})
		p += 24 + l
	}
	return out, nil
}

// fragments reassembles extended advertising data that the controller splits over several reports
// (status 1 = more follows). Returns the full data and true when a report completes it.
type fragments map[string][]byte

func (f fragments) add(r advReport) ([]byte, bool) {
	key := fmt.Sprintf("%s/%d", r.addr, r.sid)
	switch r.status {
	case 0:
		if prev, ok := f[key]; ok {
			delete(f, key)
			return append(prev, r.data...), true
		}
		return r.data, true
	case 1:
		if len(f[key])+len(r.data) <= 1650 { // max extended advertising data
			f[key] = append(f[key], r.data...)
		}
	default: // truncated: drop
		delete(f, key)
	}
	return nil, false
}

// manufacturerData returns the payload after the company ID for the given company, or nil.
// Same bytes as BlueZ's Device1.ManufacturerData[company] in the D-Bus scanner.
func manufacturerData(ad []byte, company uint16) []byte {
	for i := 0; i < len(ad); {
		l := int(ad[i])
		if l == 0 || i+1+l > len(ad) {
			return nil
		}
		if ad[i+1] == adTypeManufacturer && l >= 3 && uint16(ad[i+2])|uint16(ad[i+3])<<8 == company {
			return ad[i+4 : i+1+l]
		}
		i += 1 + l
	}
	return nil
}

// cmdResult checks whether an event completes the given command; ok=false if it is unrelated.
// ret = return parameters after the status (Command Complete only), e.g. LE features.
func cmdResult(ev []byte, opcode uint16) (status byte, ret []byte, ok bool) {
	if len(ev) < 2 {
		return 0, nil, false
	}
	switch ev[0] {
	case hciEvCmdComplete: // [0x0E, len, ncmd, op lo, op hi, status, ...]
		if len(ev) >= 6 && uint16(ev[3])|uint16(ev[4])<<8 == opcode {
			return ev[5], ev[6:], true
		}
	case hciEvCmdStatus: // [0x0F, len, status, ncmd, op lo, op hi]
		if len(ev) >= 6 && uint16(ev[4])|uint16(ev[5])<<8 == opcode {
			return ev[2], nil, true
		}
	}
	return 0, nil, false
}

// scanUnits converts a scan interval/window to 0.625 ms controller units (valid 0x0004–0x4000).
func scanUnits(ms float64, def uint16) uint16 {
	if ms <= 0 {
		return def
	}
	u := ms / 0.625 // clamp before the integer conversion (a large value would wrap around)
	if u < 4 {
		u = 4
	}
	if u > 0x4000 {
		u = 0x4000
	}
	return uint16(u)
}
