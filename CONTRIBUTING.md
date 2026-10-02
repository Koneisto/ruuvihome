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
    scanner.go         - Scanner type, mode selection, BlueZ D-Bus scanning (dbus mode)
    hci_linux.go       - Direct HCI user channel scanning (hci mode, Linux)
    hci_parse.go       - HCI event / advertising report parsing (OS independent, unit tested)
    hci_other.go       - hci mode stub for non-Linux builds
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

## Adding a Scanning Mode

1. Add the mode to `bluetooth.mode` validation in `config/config.go`
2. Implement a session function with the same contract as `runScanSession` / `runHCISession`: deliver Ruuvi
   manufacturer data (company 0x0499, without the company ID) to the handler, return an error to be restarted
3. Select it in `Scanner.Start()` in `ble/scanner.go`
4. Put protocol parsing in an OS-independent file with unit tests
5. Document the mode in `docs/BACKENDS.md`

## Reporting Issues

When reporting a bug, please include:

- Operating system and architecture
- Go version
- Docker version (if applicable)
- Scanning mode in use (`bluetooth.mode`) and the adapter model
- Relevant log output (with `logging.level: debug`)
- Configuration (with sensitive values removed)

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
