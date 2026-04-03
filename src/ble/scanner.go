package ble

import (
	"context"
	"fmt"
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
}

// NewScanner creates a new BLE scanner using the configured backend.
// The backend is selected based on config.Bluetooth.Backend:
//   - "auto" (default): tries HCI first, falls back to D-Bus if HCI fails within 5s
//   - "hci":  uses go-ble/ble with direct HCI socket access
//   - "dbus": uses BlueZ D-Bus interface via godbus
func NewScanner(cfg *config.Config, logger *config.Logger, handler AdvertisementHandler) (*Scanner, error) {
	backend := cfg.Bluetooth.Backend
	if backend == "" {
		backend = "auto"
	}

	switch backend {
	case "hci":
		logger.Info("Using HCI backend (go-ble)")
		b := newHCIScanner(cfg, logger, handler)
		return &Scanner{backend: b}, nil

	case "dbus":
		logger.Info("Using D-Bus backend (BlueZ)")
		b := newDBusScanner(cfg, logger, handler)
		return &Scanner{backend: b}, nil

	case "auto":
		logger.Info("Auto-detecting BLE backend...")
		scanner, err := autoDetectBackend(cfg, logger, handler)
		if err != nil {
			return nil, err
		}
		return &Scanner{backend: scanner}, nil

	default:
		return nil, fmt.Errorf("unknown BLE backend %q: must be \"auto\", \"hci\", or \"dbus\"", backend)
	}
}

// autoDetectBackend tries HCI first with a 5-second timeout.
// If HCI initialization fails, it falls back to D-Bus.
func autoDetectBackend(cfg *config.Config, logger *config.Logger, handler AdvertisementHandler) (backendScanner, error) {
	// Try HCI with a timeout
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
		logger.Info("HCI backend not available, falling back to D-Bus", "error", err)
	case <-time.After(5 * time.Second):
		logger.Info("HCI probe timed out after 5s, falling back to D-Bus")
	}

	// Fall back to D-Bus
	dbus := newDBusScanner(cfg, logger, handler)
	logger.Info("Using D-Bus backend (BlueZ)")
	return dbus, nil
}

// Start begins BLE scanning using the selected backend
func (s *Scanner) Start(ctx context.Context) error {
	return s.backend.Start(ctx)
}

// Stop stops the BLE scanner
func (s *Scanner) Stop() error {
	return s.backend.Stop()
}
