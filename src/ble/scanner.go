package ble

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"ruuvihome/config"
)

// RuuviManufacturerID is the Bluetooth manufacturer ID for Ruuvi Innovations
const RuuviManufacturerID = 0x0499

// AdvertisementHandler is called when a Ruuvi advertisement is received
type AdvertisementHandler func(addr string, rssi int, manufacturerData []byte)

// backendScanner is the internal interface that each backend implements
type backendScanner interface {
	Start(ctx context.Context) error
	Stop() error
}

// Scanner handles BLE scanning for Ruuvi devices.
// It delegates to the selected backend (HCI or D-Bus).
type Scanner struct {
	backend backendScanner
	// LastAdvTime stores the unix millisecond timestamp of the last received advertisement.
	// Used by the watchdog to detect if the BLE scanner has hung.
	LastAdvTime atomic.Int64
}

// NewScanner creates a new BLE scanner using the configured backend.
// The backend is selected based on config.Bluetooth.Backend:
//   - "auto" (default): tries D-Bus first (coexists with BlueZ), falls back to HCI
//   - "hci":  uses go-ble/ble with direct HCI socket access
//   - "dbus": uses BlueZ D-Bus interface via godbus
func NewScanner(cfg *config.Config, logger *config.Logger, handler AdvertisementHandler) (*Scanner, error) {
	s := &Scanner{}

	// Wrap handler to update watchdog timestamp on every advertisement
	wrappedHandler := func(addr string, rssi int, data []byte) {
		s.LastAdvTime.Store(time.Now().UnixMilli())
		handler(addr, rssi, data)
	}

	backend := cfg.Bluetooth.Backend
	if backend == "" {
		backend = "auto"
	}

	switch backend {
	case "hci":
		logger.Info("Using HCI backend (go-ble)")
		s.backend = newHCIScanner(cfg, logger, wrappedHandler)
		return s, nil

	case "dbus":
		logger.Info("Using D-Bus backend (BlueZ)")
		s.backend = newDBusScanner(cfg, logger, wrappedHandler)
		return s, nil

	case "auto":
		logger.Info("Auto-detecting BLE backend...")
		b, err := autoDetectBackend(cfg, logger, wrappedHandler)
		if err != nil {
			return nil, err
		}
		s.backend = b
		return s, nil

	default:
		return nil, fmt.Errorf("unknown BLE backend %q: must be \"auto\", \"hci\", or \"dbus\"", backend)
	}
}

// autoDetectBackend tries D-Bus first (cooperative with BlueZ), then falls
// back to HCI (takes exclusive control of the adapter).
//
// D-Bus is preferred because it coexists with BlueZ — this avoids the race
// condition where HCI probe succeeds but the subsequent scan conflicts with
// BlueZ, which is the default on Raspberry Pi and most desktop Linux systems.
func autoDetectBackend(cfg *config.Config, logger *config.Logger, handler AdvertisementHandler) (backendScanner, error) {
	// Try D-Bus first — cooperative with BlueZ
	if probeDBus() {
		logger.Info("BlueZ adapter found via D-Bus, using D-Bus backend")
		return newDBusScanner(cfg, logger, handler), nil
	}
	logger.Info("D-Bus/BlueZ not available, trying HCI backend")

	// Fall back to HCI — takes exclusive control of the adapter
	hci := newHCIScanner(cfg, logger, handler)

	probeCh := make(chan error, 1)
	go func() {
		probeCh <- hci.Probe()
	}()

	select {
	case err := <-probeCh:
		if err == nil {
			logger.Info("HCI backend available, using HCI")
			return hci, nil
		}
		return nil, fmt.Errorf("no BLE backend available: D-Bus/BlueZ not found, HCI failed: %w", err)
	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("no BLE backend available: D-Bus/BlueZ not found, HCI probe timed out")
	}
}

// Start begins BLE scanning using the selected backend
func (s *Scanner) Start(ctx context.Context) error {
	// Seed watchdog timer so it doesn't fire during startup
	s.LastAdvTime.Store(time.Now().UnixMilli())
	return s.backend.Start(ctx)
}

// Stop stops the BLE scanner
func (s *Scanner) Stop() error {
	return s.backend.Stop()
}
