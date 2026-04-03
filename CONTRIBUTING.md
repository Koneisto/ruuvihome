# Contributing to ruuvihome

Contributions are welcome. This document covers the essentials for getting started.

## Getting Started

1. Fork and clone the repository
2. Copy `config.example.yml` to `config.yml` and configure for your environment
3. Build: `cd src && CGO_ENABLED=1 go build -o ruuvihome .`
4. Run: `./ruuvihome -config ../config.yml`

## Development Requirements

- Go 1.22 or later
- Linux with BlueZ (for BLE support)
- A Ruuvi sensor for end-to-end testing
- Docker (optional, for container builds)

## Project Structure

```
src/
  main.go              - Application entry point
  ble/
    scanner.go         - Backend selector and Scanner type
    scanner_hci.go     - HCI backend (go-ble/ble)
    scanner_dbus.go    - D-Bus backend (godbus/dbus)
  config/
    config.go          - Configuration loading and validation
  parser/
    parser.go          - Format dispatcher and extended values
    format5.go         - Ruuvi Format 5 (RAWv2) parser
    format6.go         - Ruuvi Format 6 (Air) parser
    format_e1.go       - Ruuvi Format E1 parser
  mqtt/
    publisher.go       - MQTT client and rate limiter
  homeassistant/
    discovery.go       - HA MQTT Discovery
```

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Keep functions short and focused
- Use descriptive variable names
- Add comments for exported types and functions
- All code, comments, and documentation must be in English

## Making Changes

1. Create a feature branch from `main`
2. Make your changes with clear commit messages
3. Ensure the code compiles: `go build ./...`
4. Run the linter: `go vet ./...`
5. Test with a real Ruuvi sensor if possible
6. Submit a pull request

## Adding a New BLE Backend

To add a new backend:

1. Create `src/ble/scanner_yourbackend.go`
2. Implement the `backendScanner` interface (defined in `scanner.go`):
   - `Start(ctx context.Context) error`
   - `Stop() error`
3. Add the backend option to `NewScanner()` in `scanner.go`
4. Add validation in `config.go`
5. Document the backend in `docs/BACKENDS.md`

## Reporting Issues

When reporting a bug, please include:

- Operating system and architecture
- Go version
- Docker version (if applicable)
- BLE backend in use (hci/dbus/auto)
- Relevant log output (with `logging.level: debug`)
- Configuration (with sensitive values removed)

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
