package ble

import (
	"context"
	"fmt"

	goble "github.com/go-ble/ble"
	"github.com/go-ble/ble/linux"

	"ruuvihome/config"
)

// hciScanner uses go-ble/ble for direct HCI socket access.
// This backend requires CAP_NET_ADMIN and CAP_NET_RAW capabilities
// and uses host networking in Docker.
type hciScanner struct {
	cfg     *config.Config
	logger  *config.Logger
	handler AdvertisementHandler
	device  goble.Device
}

// newHCIScanner creates a new HCI-based BLE scanner
func newHCIScanner(cfg *config.Config, logger *config.Logger, handler AdvertisementHandler) *hciScanner {
	return &hciScanner{
		cfg:     cfg,
		logger:  logger,
		handler: handler,
	}
}

// Probe checks whether the HCI device can be initialized.
// It opens and immediately closes the device to verify access.
func (s *hciScanner) Probe() error {
	device, err := linux.NewDevice(goble.OptDeviceID(s.cfg.Bluetooth.HCIDevice))
	if err != nil {
		return fmt.Errorf("HCI probe failed on hci%d: %w", s.cfg.Bluetooth.HCIDevice, err)
	}
	// Close the device immediately; Start() will reopen it
	return device.Stop()
}

// Start begins BLE scanning using direct HCI access
func (s *hciScanner) Start(ctx context.Context) error {
	deviceName := fmt.Sprintf("hci%d", s.cfg.Bluetooth.HCIDevice)
	device, err := linux.NewDevice(goble.OptDeviceID(s.cfg.Bluetooth.HCIDevice))
	if err != nil {
		return fmt.Errorf("failed to initialize %s: %w", deviceName, err)
	}
	s.device = device

	// Set as default device for go-ble
	goble.SetDefaultDevice(device)

	s.logger.Info("Scanning BLE advertisements (HCI backend)...", "device", deviceName)

	advHandler := func(a goble.Advertisement) {
		s.handleAdvertisement(a)
	}

	// Start passive scanning (blocks until context is cancelled)
	err = goble.Scan(ctx, true, advHandler, nil)
	if err != nil && err != context.Canceled {
		return fmt.Errorf("scan error: %w", err)
	}

	return nil
}

// handleAdvertisement processes a single BLE advertisement from go-ble
func (s *hciScanner) handleAdvertisement(a goble.Advertisement) {
	manufacturerData := a.ManufacturerData()
	if len(manufacturerData) < 3 {
		return
	}

	// Check for Ruuvi manufacturer ID (little-endian)
	manufacturerID := uint16(manufacturerData[0]) | uint16(manufacturerData[1])<<8
	if manufacturerID != RuuviManufacturerID {
		return
	}

	// Extract payload (skip 2-byte manufacturer ID)
	payload := manufacturerData[2:]
	if len(payload) < 1 {
		return
	}

	addr := a.Addr().String()
	rssi := a.RSSI()

	s.logger.Trace("Ruuvi advertisement received (HCI)",
		"mac", addr,
		"rssi", rssi,
		"format", payload[0],
		"data_len", len(payload))

	s.handler(addr, rssi, payload)
}

// Stop stops the HCI BLE scanner
func (s *hciScanner) Stop() error {
	if s.device != nil {
		return s.device.Stop()
	}
	return nil
}
