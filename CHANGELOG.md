# Changelog

All notable changes to ruuvihome are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/).

## [1.1.0] - 2025-04-02

### Added
- Dual BLE backend support: HCI (go-ble) and D-Bus (BlueZ/godbus)
- `bluetooth.backend` configuration option (`auto`, `hci`, `dbus`)
- Auto-detection mode that tries HCI first, falls back to D-Bus
- D-Bus backend with automatic BlueZ adapter discovery
- D-Bus socket mount in docker-compose.yml for BlueZ integration
- Backend documentation (docs/BACKENDS.md)
- Troubleshooting guide (docs/TROUBLESHOOTING.md)
- Contributing guide (CONTRIBUTING.md)

### Changed
- Moved HCI scanner to dedicated file (scanner_hci.go)
- Dockerfile now includes bluez-dev for D-Bus backend compilation
- Updated README with backend selection and platform compatibility

## [1.0.0] - 2025-03-15

### Added
- Initial release
- BLE scanning via go-ble/ble (HCI backend)
- MQTT publishing with per-device rate limiting
- Home Assistant MQTT Discovery with automatic sensor creation
- Ruuvi Format 5 (RAWv2) support for RuuviTag
- Ruuvi Format 6 support for Ruuvi Air (PM2.5, CO2, VOC, NOx)
- Ruuvi Format E1 parsing (pending BLE library extended advertising support)
- Calculated values: dew point, absolute humidity, air density, AQI
- Per-device configuration with friendly names and custom intervals
- Device filtering (named or all modes)
- Structured logging (simple and JSON formats)
- Multi-stage Docker build with UPX compression (~1.9MB image)
- Security-hardened container (dropped capabilities, read-only filesystem)
