package ble

import (
	"context"
	"fmt"

	"github.com/go-ble/ble"
	"github.com/go-ble/ble/linux"

	"ruuvihome/config"
)

// RuuviManufacturerID is the Bluetooth manufacturer ID for Ruuvi Innovations
const RuuviManufacturerID = 0x0499

// AdvertisementHandler is called when a Ruuvi advertisement is received
type AdvertisementHandler func(addr string, rssi int, manufacturerData []byte)

// Scanner handles BLE scanning for Ruuvi devices
type Scanner struct {
	cfg     *config.Config
	logger  *config.Logger
	handler AdvertisementHandler
	device  ble.Device
}

// NewScanner creates a new BLE scanner
func NewScanner(cfg *config.Config, logger *config.Logger, handler AdvertisementHandler) (*Scanner, error) {
	return &Scanner{
		cfg:     cfg,
		logger:  logger,
		handler: handler,
	}, nil
}

// Start begins BLE scanning
func (s *Scanner) Start(ctx context.Context) error {
	// Initialize the HCI device
	deviceName := fmt.Sprintf("hci%d", s.cfg.Bluetooth.HCIDevice)
	device, err := linux.NewDevice(ble.OptDeviceID(s.cfg.Bluetooth.HCIDevice))
	if err != nil {
		return fmt.Errorf("failed to initialize %s: %w", deviceName, err)
	}
	s.device = device

	// Set as default device
	ble.SetDefaultDevice(device)

	s.logger.Info("Scanning BLE advertisements...", "device", deviceName)

	// Create advertisement handler
	advHandler := func(a ble.Advertisement) {
		s.handleAdvertisement(a)
	}

	// Start passive scanning
	// This will block until context is cancelled
	err = ble.Scan(ctx, true, advHandler, nil)
	if err != nil && err != context.Canceled {
		return fmt.Errorf("scan error: %w", err)
	}

	return nil
}

// handleAdvertisement processes a single BLE advertisement
func (s *Scanner) handleAdvertisement(a ble.Advertisement) {
	// Get manufacturer data
	manufacturerData := a.ManufacturerData()
	if len(manufacturerData) < 3 {
		return
	}

	// Check for Ruuvi manufacturer ID (little-endian)
	// Manufacturer data format: [ID_low, ID_high, payload...]
	manufacturerID := uint16(manufacturerData[0]) | uint16(manufacturerData[1])<<8
	if manufacturerID != RuuviManufacturerID {
		return
	}

	// Extract payload (skip manufacturer ID)
	payload := manufacturerData[2:]
	if len(payload) < 1 {
		return
	}

	// Get MAC address as string
	addr := a.Addr().String()

	// Get RSSI
	rssi := a.RSSI()

	s.logger.Trace("Ruuvi advertisement received",
		"mac", addr,
		"rssi", rssi,
		"format", payload[0],
		"data_len", len(payload))

	// Call the handler with the payload
	s.handler(addr, rssi, payload)
}

// Stop stops the BLE scanner
func (s *Scanner) Stop() error {
	if s.device != nil {
		return s.device.Stop()
	}
	return nil
}
