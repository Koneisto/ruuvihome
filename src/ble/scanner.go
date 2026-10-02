package ble

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"

	"ruuvihome/config"
)

// RuuviManufacturerID is the Bluetooth manufacturer ID for Ruuvi Innovations
const RuuviManufacturerID = 0x0499

// Tuning constants for the BLE scan loop.
const (
	// signalChBuffer is the D-Bus signal channel buffer size.
	// 500 is enough to absorb bursts (e.g. 9 Ruuvi tags advertising 10/s = 90 ev/s)
	// without blocking the D-Bus dispatcher even if the consumer briefly stalls.
	signalChBuffer = 500

	// bluezStopSettleDelay gives BlueZ time to release the previous discovery
	// before starting a new one. Empirically 500 ms avoids "Operation in progress" errors.
	bluezStopSettleDelay = 500 * time.Millisecond

	// scanRestartDelay is how long we wait after a failed scan session
	// before retrying. Short enough to recover quickly, long enough to avoid spam.
	scanRestartDelay = 5 * time.Second

	// signalSilenceTimeout: if no Ruuvi advertisement for this long, the
	// session is considered dead and a fresh D-Bus connection is opened.
	signalSilenceTimeout = 3 * time.Minute

	// signalCheckInterval: how often the watchdog wakes up to check silence.
	signalCheckInterval = 10 * time.Second
)

// AdvertisementHandler is called when a Ruuvi advertisement is received
type AdvertisementHandler func(addr string, rssi int, manufacturerData []byte)

// Scanner handles BLE scanning for Ruuvi devices using BlueZ D-Bus
type Scanner struct {
	cfg     *config.Config
	logger  *config.Logger
	handler AdvertisementHandler
	bus     *dbus.Conn
	adapter dbus.ObjectPath
	// lastRSSI caches the latest RSSI per device path. BlueZ's PropertiesChanged omits RSSI when it did not
	// change; the cache avoids a synchronous D-Bus GetProperty round-trip for every such advertisement.
	lastRSSI map[dbus.ObjectPath]int
}

// NewScanner creates a new BLE scanner
func NewScanner(cfg *config.Config, logger *config.Logger, handler AdvertisementHandler) (*Scanner, error) {
	return &Scanner{
		cfg:     cfg,
		logger:  logger,
		handler: handler,
	}, nil
}

// findAdapter auto-detects the first available BlueZ adapter via D-Bus
func (s *Scanner) findAdapter() (dbus.ObjectPath, error) {
	bluez := s.bus.Object("org.bluez", "/")

	var result map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	err := bluez.Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&result)
	if err != nil {
		return "", fmt.Errorf("failed to list BlueZ objects: %w", err)
	}

	for path, ifaces := range result {
		if _, ok := ifaces["org.bluez.Adapter1"]; ok {
			return path, nil
		}
	}

	return "", fmt.Errorf("no BlueZ adapter found")
}

// Start begins BLE scanning using D-Bus signals with automatic recovery
func (s *Scanner) Start(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		var err error
		if s.cfg.Bluetooth.Mode == "hci" { // direct HCI, continuous passive scan (hci_linux.go)
			err = s.runHCISession(ctx)
		} else {
			err = s.runScanSession(ctx)
		}
		if err != nil && ctx.Err() == nil {
			s.logger.Info("Scan session ended, restarting", "error", err, "delay", scanRestartDelay)
			time.Sleep(scanRestartDelay)
			continue
		}
		return err
	}
}

// runScanSession runs a single scan session, returns error if signals stop
func (s *Scanner) runScanSession(ctx context.Context) error {
	bus, err := dbus.SystemBus()
	if err != nil {
		return fmt.Errorf("failed to connect to system D-Bus: %w", err)
	}
	s.bus = bus
	defer bus.Close()

	adapterPath, err := s.findAdapter()
	if err != nil {
		return fmt.Errorf("failed to find BLE adapter: %w", err)
	}
	s.adapter = adapterPath
	s.logger.Info("Found BLE adapter", "path", string(adapterPath))

	adapter := s.bus.Object("org.bluez", adapterPath)

	// Power on
	adapter.SetProperty("org.bluez.Adapter1.Powered", dbus.MakeVariant(true))

	// Set discovery filter
	filter := map[string]dbus.Variant{
		"Transport":     dbus.MakeVariant("le"),
		"DuplicateData": dbus.MakeVariant(true),
	}
	adapter.Call("org.bluez.Adapter1.SetDiscoveryFilter", 0, filter)

	// Stop any old discovery, start fresh
	adapter.Call("org.bluez.Adapter1.StopDiscovery", 0)
	time.Sleep(bluezStopSettleDelay)

	// Subscribe to D-Bus signals (narrowed to configured tags in filter_mode=named, see matchRules)
	s.lastRSSI = make(map[dbus.ObjectPath]int)
	rules := s.matchRules()
	for _, rule := range rules {
		if err := s.bus.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, rule).Err; err != nil {
			return fmt.Errorf("AddMatch %q: %w", rule, err)
		}
	}
	s.logger.Info("D-Bus match rules set", "rules", len(rules), "filter_mode", s.cfg.Processing.FilterMode)

	signalCh := make(chan *dbus.Signal, signalChBuffer)
	s.bus.Signal(signalCh)

	err = adapter.Call("org.bluez.Adapter1.StartDiscovery", 0).Err
	if err != nil {
		return fmt.Errorf("failed to start discovery: %w", err)
	}
	s.logger.Info("BLE discovery started")

	defer func() {
		adapter.Call("org.bluez.Adapter1.StopDiscovery", 0)
		s.bus.RemoveSignal(signalCh)
	}()

	// Process signals with timeout detection
	lastSignal := time.Now()
	timeoutCheck := time.NewTicker(signalCheckInterval)
	defer timeoutCheck.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil

		case sig := <-signalCh:
			if s.processSignal(sig) {
				lastSignal = time.Now()
			}

		case <-timeoutCheck.C:
			if time.Since(lastSignal) > signalSilenceTimeout {
				return fmt.Errorf("no BLE signals for %v", signalSilenceTimeout)
			}
		}
	}
}

// matchRules returns the D-Bus match rules for this session.
//
// With filter_mode=named only the configured tags' device objects are subscribed, so dbus-daemon drops the
// advertisements of every other nearby BLE device (phones, TVs, beacons) before they reach this process.
// With DuplicateData=true BlueZ emits a PropertiesChanged signal per advertisement, so in a busy radio
// environment the broad rule made the scanner wake and decode dozens of irrelevant signals per second
// (measured ~35 % of a Pi 3B+ core). filter_mode=all keeps the original broad rule.
func (s *Scanner) matchRules() []string {
	if s.cfg.Processing.FilterMode != "named" || len(s.cfg.Devices) == 0 {
		return []string{"type='signal',sender='org.bluez'"}
	}
	rules := make([]string, 0, 2*len(s.cfg.Devices))
	for mac := range s.cfg.Devices {
		path := devicePath(s.adapter, mac)
		rules = append(rules,
			fmt.Sprintf("type='signal',sender='org.bluez',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged',path='%s'", path),
			fmt.Sprintf("type='signal',sender='org.bluez',interface='org.freedesktop.DBus.ObjectManager',member='InterfacesAdded',arg0path='%s'", path))
	}
	return rules
}

// devicePath maps a MAC address to its BlueZ object path, e.g. /org/bluez/hci0/dev_C8_8A_AB_3D_7B_E8.
func devicePath(adapter dbus.ObjectPath, mac string) string {
	return string(adapter) + "/dev_" + strings.ReplaceAll(strings.ToUpper(mac), ":", "_")
}

// processSignal handles D-Bus signals, returns true if it was a Ruuvi advertisement
func (s *Scanner) processSignal(sig *dbus.Signal) bool {
	switch sig.Name {
	case "org.freedesktop.DBus.Properties.PropertiesChanged":
		if len(sig.Body) < 2 {
			return false
		}
		iface, ok := sig.Body[0].(string)
		if !ok || iface != "org.bluez.Device1" {
			return false
		}
		changed, ok := sig.Body[1].(map[string]dbus.Variant)
		if !ok {
			return false
		}
		return s.handleDeviceProperties(sig.Path, changed)

	case "org.freedesktop.DBus.ObjectManager.InterfacesAdded":
		if len(sig.Body) < 2 {
			return false
		}
		path, ok := sig.Body[0].(dbus.ObjectPath)
		if !ok {
			return false
		}
		ifaces, ok := sig.Body[1].(map[string]map[string]dbus.Variant)
		if !ok {
			return false
		}
		if props, ok := ifaces["org.bluez.Device1"]; ok {
			return s.handleDeviceProperties(path, props)
		}
	}
	return false
}

// handleDeviceProperties extracts Ruuvi data, returns true if found
func (s *Scanner) handleDeviceProperties(path dbus.ObjectPath, props map[string]dbus.Variant) bool {
	mfgDataVar, ok := props["ManufacturerData"]
	if !ok {
		return false
	}

	mfgData, ok := mfgDataVar.Value().(map[uint16]dbus.Variant)
	if !ok {
		return false
	}

	ruuviVar, ok := mfgData[RuuviManufacturerID]
	if !ok {
		return false
	}

	payload, ok := ruuviVar.Value().([]byte)
	if !ok || len(payload) < 1 {
		return false
	}

	addr := extractMAC(string(path))
	if addr == "" {
		return false
	}

	rssi := 0
	if rssiVar, ok := props["RSSI"]; ok {
		if v, ok := rssiVar.Value().(int16); ok {
			rssi = int(v)
		}
	} else if cached, ok := s.lastRSSI[path]; ok {
		rssi = cached // RSSI unchanged since the previous advertisement
	} else if s.bus != nil {
		device := s.bus.Object("org.bluez", path)
		rssiProp, err := device.GetProperty("org.bluez.Device1.RSSI")
		if err == nil {
			if v, ok := rssiProp.Value().(int16); ok {
				rssi = int(v)
			}
		}
	}

	if s.lastRSSI != nil && rssi != 0 {
		s.lastRSSI[path] = rssi
	}

	s.logger.Trace("Ruuvi advertisement received",
		"mac", addr,
		"rssi", rssi,
		"format", payload[0],
		"data_len", len(payload))

	s.handler(addr, rssi, payload)
	return true
}

// extractMAC extracts MAC address from BlueZ D-Bus path
func extractMAC(path string) string {
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if strings.HasPrefix(part, "dev_") {
			mac := strings.TrimPrefix(part, "dev_")
			return strings.ReplaceAll(mac, "_", ":")
		}
	}
	return ""
}
