package ble

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"

	"ruuvihome/config"
)

// Discovery keepalive interval — BlueZ may stop discovery automatically
// after ~3 minutes. Restarting it periodically ensures continuous scanning.
const discoveryKeepalive = 2 * time.Minute

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

// probeDBus checks whether BlueZ is available on the system D-Bus and has
// at least one BLE adapter. This is used by auto-detection to decide
// whether to use the D-Bus backend.
func probeDBus() bool {
	bus, err := dbus.SystemBus()
	if err != nil {
		return false
	}
	defer bus.Close()

	bluez := bus.Object("org.bluez", "/")
	var result map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	if err := bluez.Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&result); err != nil {
		return false
	}

	for _, ifaces := range result {
		if _, ok := ifaces["org.bluez.Adapter1"]; ok {
			return true
		}
	}
	return false
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

	// Set discovery filter for BLE only with duplicate data enabled.
	// DuplicateData is critical for continuous sensor monitoring —
	// without it, BlueZ only reports each device once per discovery session.
	filter := map[string]dbus.Variant{
		"Transport":     dbus.MakeVariant("le"),
		"DuplicateData": dbus.MakeVariant(true),
	}
	err = adapter.Call("org.bluez.Adapter1.SetDiscoveryFilter", 0, filter).Err
	if err != nil {
		// DuplicateData may not be supported on older BlueZ (<5.56).
		// Try without it — we'll use keepalive restarts as a workaround.
		s.logger.Warn("SetDiscoveryFilter failed, retrying without DuplicateData", "error", err)
		filter = map[string]dbus.Variant{
			"Transport": dbus.MakeVariant("le"),
		}
		if err2 := adapter.Call("org.bluez.Adapter1.SetDiscoveryFilter", 0, filter).Err; err2 != nil {
			s.logger.Warn("SetDiscoveryFilter failed completely", "error", err2)
		}
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

	// Keepalive timer — restart discovery periodically.
	// BlueZ may stop discovery after ~3 minutes on some systems.
	keepalive := time.NewTicker(discoveryKeepalive)
	defer keepalive.Stop()

	// Process signals until context is cancelled
	for {
		select {
		case <-ctx.Done():
			return nil
		case sig := <-signalCh:
			s.processSignal(sig)
		case <-keepalive.C:
			// Restart discovery to keep it active
			adapter.Call("org.bluez.Adapter1.StopDiscovery", 0)
			if err := adapter.Call("org.bluez.Adapter1.StartDiscovery", 0).Err; err != nil {
				s.logger.Warn("Discovery keepalive restart failed", "error", err)
			} else {
				s.logger.Debug("Discovery keepalive restart")
			}
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

// handleDeviceProperties extracts Ruuvi data from device properties.
// Handles multiple D-Bus type formats for ManufacturerData across
// different BlueZ versions.
func (s *dbusScanner) handleDeviceProperties(path dbus.ObjectPath, props map[string]dbus.Variant) {
	// Get ManufacturerData
	mfgDataVar, ok := props["ManufacturerData"]
	if !ok {
		return
	}

	s.logger.Trace("ManufacturerData signal", "path", string(path), "type", fmt.Sprintf("%T", mfgDataVar.Value()))

	// Extract Ruuvi payload — handle multiple D-Bus type representations
	// that different BlueZ versions may use.
	payload := s.extractRuuviPayload(mfgDataVar)
	if payload == nil {
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

// extractRuuviPayload extracts the Ruuvi payload from ManufacturerData,
// handling the different D-Bus type representations:
//   - map[uint16]dbus.Variant  (BlueZ 5.55+, common)
//   - map[uint16][]byte        (some BlueZ versions)
func (s *dbusScanner) extractRuuviPayload(mfgDataVar dbus.Variant) []byte {
	switch mfgData := mfgDataVar.Value().(type) {
	case map[uint16]dbus.Variant:
		ruuviVar, ok := mfgData[RuuviManufacturerID]
		if !ok {
			return nil
		}
		payload, ok := ruuviVar.Value().([]byte)
		if !ok || len(payload) < 1 {
			return nil
		}
		return payload

	case map[uint16][]byte:
		payload, ok := mfgData[RuuviManufacturerID]
		if !ok || len(payload) < 1 {
			return nil
		}
		return payload

	default:
		s.logger.Debug("Unsupported ManufacturerData D-Bus type",
			"type", fmt.Sprintf("%T", mfgDataVar.Value()))
		return nil
	}
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
