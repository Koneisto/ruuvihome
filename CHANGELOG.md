# Changelog

All notable changes to ruuvihome are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/).

## [1.2.1] - 2026-10-18

### Security
- Update MQTT/WebSocket/network dependencies to patched versions.
- Build with pinned Go 1.27.2; verify modules and prohibit implicit dependency changes during builds.
- Keep scanner, MQTT payloads and deployment configuration unchanged.
- Verified with `go test ./...`, `go vet ./...` and `govulncheck ./...`.

## [1.2.0] - 2026-10-03

### Added
- `bluetooth.mode: hci`: direct HCI user channel scanning. The controller is reset once and passive scanning is enabled
  once and never stopped (BlueZ discovery restarts scanning every 10.24 s, which hangs some controllers such as the
  Broadcom BCM20702). Bluetooth 5 adapters use extended scanning on LE 1M and LE Coded, so Ruuvi Air format E1 is
  received; fragmented extended advertising data is reassembled. 60 s without Ruuvi advertisements resets the controller.
- Ruuvi Air: format E1 is preferred over format 6 from the same device, so published values do not alternate.
- `/health` endpoint on localhost:9100 and `-healthcheck` flag; Docker HEALTHCHECK in docker-compose.yml.
- Stats log every 5 min and per-tag silence warning after 15 min.
- Unit tests for the parser, HCI report parsing and configuration.

### Changed
- Pure-Go static build (`CGO_ENABLED=0`); D-Bus scanning via godbus.
- MQTT publishing in its own goroutine; one retry per publish.
- With `filter_mode: named`, D-Bus signals are subscribed only for the configured tags.
- `bluetooth.backend` (v1.1.0) is accepted as an alias when `mode` is not set (`auto` selects dbus).

### Removed
- go-ble based HCI backend and backend auto-detection (replaced by `bluetooth.mode`).

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
