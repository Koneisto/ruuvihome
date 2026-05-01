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

// AdvertisementHandler is called when a Ruuvi advertisement is received
type AdvertisementHandler func(addr string, rssi int, manufacturerData []byte)

// Scanner handles BLE scanning for Ruuvi devices using BlueZ D-Bus
type Scanner struct {
	cfg     *config.Config
	logger  *config.Logger
	handler AdvertisementHandler
	bus     *dbus.Conn
	adapter dbus.ObjectPath
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

		err := s.runScanSession(ctx)
		if err != nil && ctx.Err() == nil {
			s.logger.Info("Scan session ended, restarting in 5s", "error", err)
			time.Sleep(5 * time.Second)
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
	time.Sleep(500 * time.Millisecond)

	// Subscribe to D-Bus signals
	matchRule := "type='signal',sender='org.bluez'"
	s.bus.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, matchRule)

	signalCh := make(chan *dbus.Signal, 500)
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
	timeoutCheck := time.NewTicker(10 * time.Second)
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
			// If no Ruuvi signal for 3 min, session is dead
			if time.Since(lastSignal) > 3*time.Minute {
				return fmt.Errorf("no BLE signals for 3 minutes")
			}
		}
	}
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
	} else if s.bus != nil {
		device := s.bus.Object("org.bluez", path)
		rssiProp, err := device.GetProperty("org.bluez.Device1.RSSI")
		if err == nil {
			if v, ok := rssiProp.Value().(int16); ok {
				rssi = int(v)
			}
		}
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

// Stop stops the BLE scanner
func (s *Scanner) Stop() error {
	if s.bus != nil {
		if s.adapter != "" {
			adapter := s.bus.Object("org.bluez", s.adapter)
			adapter.Call("org.bluez.Adapter1.StopDiscovery", 0)
		}
		return s.bus.Close()
	}
	return nil
}
