package ble

import (
	"context"
	"fmt"
	"strings"

	"github.com/godbus/dbus/v5"

	"ruuvihome/config"
)

// dbusScanner uses the BlueZ D-Bus interface for BLE scanning.
// This backend does not require raw HCI access and works well
// on systems where BlueZ manages the Bluetooth adapter (e.g., desktop Linux,
// Raspberry Pi OS). It requires the system D-Bus socket to be accessible.
type dbusScanner struct {
	cfg     *config.Config
	logger  *config.Logger
	handler AdvertisementHandler
	bus     *dbus.Conn
	adapter dbus.ObjectPath
}

// newDBusScanner creates a new D-Bus based BLE scanner
func newDBusScanner(cfg *config.Config, logger *config.Logger, handler AdvertisementHandler) *dbusScanner {
	return &dbusScanner{
		cfg:     cfg,
		logger:  logger,
		handler: handler,
	}
}

// findAdapter auto-detects the first available BlueZ adapter via D-Bus
func (s *dbusScanner) findAdapter() (dbus.ObjectPath, error) {
	bluez := s.bus.Object("org.bluez", "/")

	var result map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	err := bluez.Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&result)
	if err != nil {
		return "", fmt.Errorf("failed to list BlueZ objects: %w", err)
	}

	// Find first adapter (has org.bluez.Adapter1 interface)
	for path, ifaces := range result {
		if _, ok := ifaces["org.bluez.Adapter1"]; ok {
			return path, nil
		}
	}

	return "", fmt.Errorf("no BlueZ adapter found")
}

// Start begins BLE scanning using the BlueZ D-Bus interface
func (s *dbusScanner) Start(ctx context.Context) error {
	bus, err := dbus.SystemBus()
	if err != nil {
		return fmt.Errorf("failed to connect to system D-Bus: %w", err)
	}
	s.bus = bus

	// Auto-detect adapter
	adapterPath, err := s.findAdapter()
	if err != nil {
		return fmt.Errorf("failed to find BLE adapter: %w", err)
	}
	s.adapter = adapterPath
	s.logger.Info("Found BLE adapter (D-Bus backend)", "path", string(adapterPath))

	adapter := s.bus.Object("org.bluez", adapterPath)

	// Ensure adapter is powered on
	err = adapter.SetProperty("org.bluez.Adapter1.Powered", dbus.MakeVariant(true))
	if err != nil {
		s.logger.Info("Could not power on adapter (may already be on)", "error", err)
	}

	// Set discovery filter for BLE only with duplicate data enabled
	filter := map[string]dbus.Variant{
		"Transport":     dbus.MakeVariant("le"),
		"DuplicateData": dbus.MakeVariant(true),
	}
	err = adapter.Call("org.bluez.Adapter1.SetDiscoveryFilter", 0, filter).Err
	if err != nil {
		s.logger.Info("Could not set discovery filter", "error", err)
	}

	// Subscribe to BlueZ signals (InterfacesAdded and PropertiesChanged)
	matchRule := "type='signal',sender='org.bluez'"
	s.bus.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, matchRule)

	signalCh := make(chan *dbus.Signal, 100)
	s.bus.Signal(signalCh)

	// Start discovery
	err = adapter.Call("org.bluez.Adapter1.StartDiscovery", 0).Err
	if err != nil {
		return fmt.Errorf("failed to start discovery: %w", err)
	}
	s.logger.Info("BLE discovery started (D-Bus backend)")

	defer func() {
		adapter.Call("org.bluez.Adapter1.StopDiscovery", 0)
		s.bus.RemoveSignal(signalCh)
	}()

	// Process signals until context is cancelled
	for {
		select {
		case <-ctx.Done():
			return nil
		case sig := <-signalCh:
			s.processSignal(sig)
		}
	}
}

// processSignal handles D-Bus signals from BlueZ
func (s *dbusScanner) processSignal(sig *dbus.Signal) {
	switch sig.Name {
	case "org.freedesktop.DBus.Properties.PropertiesChanged":
		if len(sig.Body) < 2 {
			return
		}
		iface, ok := sig.Body[0].(string)
		if !ok || iface != "org.bluez.Device1" {
			return
		}
		changed, ok := sig.Body[1].(map[string]dbus.Variant)
		if !ok {
			return
		}
		s.handleDeviceProperties(sig.Path, changed)

	case "org.freedesktop.DBus.ObjectManager.InterfacesAdded":
		if len(sig.Body) < 2 {
			return
		}
		path, ok := sig.Body[0].(dbus.ObjectPath)
		if !ok {
			return
		}
		ifaces, ok := sig.Body[1].(map[string]map[string]dbus.Variant)
		if !ok {
			return
		}
		if props, ok := ifaces["org.bluez.Device1"]; ok {
			s.handleDeviceProperties(path, props)
		}
	}
}

// handleDeviceProperties extracts Ruuvi data from device properties
func (s *dbusScanner) handleDeviceProperties(path dbus.ObjectPath, props map[string]dbus.Variant) {
	// Get ManufacturerData
	mfgDataVar, ok := props["ManufacturerData"]
	if !ok {
		return
	}

	s.logger.Trace("ManufacturerData signal", "path", string(path), "type", fmt.Sprintf("%T", mfgDataVar.Value()))

	mfgData, ok := mfgDataVar.Value().(map[uint16]dbus.Variant)
	if !ok {
		return
	}

	// Check for Ruuvi manufacturer ID
	ruuviVar, ok := mfgData[RuuviManufacturerID]
	if !ok {
		return
	}

	payload, ok := ruuviVar.Value().([]byte)
	if !ok || len(payload) < 1 {
		return
	}

	// Extract MAC from D-Bus path: /org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF
	addr := extractMAC(string(path))
	if addr == "" {
		return
	}

	// Get RSSI from signal properties or query the device object
	rssi := 0
	if rssiVar, ok := props["RSSI"]; ok {
		if v, ok := rssiVar.Value().(int16); ok {
			rssi = int(v)
		}
	} else {
		device := s.bus.Object("org.bluez", path)
		rssiProp, err := device.GetProperty("org.bluez.Device1.RSSI")
		if err == nil {
			if v, ok := rssiProp.Value().(int16); ok {
				rssi = int(v)
			}
		}
	}

	s.logger.Trace("Ruuvi advertisement received (D-Bus)",
		"mac", addr,
		"rssi", rssi,
		"format", payload[0],
		"data_len", len(payload))

	// Send payload directly (format byte + data, no manufacturer ID prefix)
	s.handler(addr, rssi, payload)
}

// extractMAC extracts a MAC address from a BlueZ D-Bus object path.
// Example: /org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF -> AA:BB:CC:DD:EE:FF
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

// Stop stops the D-Bus BLE scanner
func (s *dbusScanner) Stop() error {
	if s.bus != nil {
		if s.adapter != "" {
			adapter := s.bus.Object("org.bluez", s.adapter)
			adapter.Call("org.bluez.Adapter1.StopDiscovery", 0)
		}
		return s.bus.Close()
	}
	return nil
}
