# BLE Backends

ruuvihome supports two Bluetooth Low Energy backends for receiving Ruuvi sensor advertisements. You can select the backend via the `bluetooth.backend` configuration option.

## Backend Overview

| Feature | HCI | D-Bus |
|---------|-----|-------|
| Library | go-ble/ble | godbus/dbus/v5 |
| BlueZ required | No (direct HCI) | Yes (BlueZ daemon) |
| Capabilities needed | `CAP_NET_ADMIN`, `CAP_NET_RAW` | System D-Bus access |
| Host networking | Required | Recommended |
| Adapter selection | `hci_device` index | Auto-detected |
| Docker scratch image | Yes | Yes (with D-Bus socket mount) |

## HCI Backend

The HCI backend communicates directly with the Bluetooth controller through Linux HCI (Host Controller Interface) sockets. It bypasses BlueZ entirely.

**When to use:**
- Dedicated server or container that owns the Bluetooth adapter
- No other application needs the adapter simultaneously
- Minimal dependency footprint (no BlueZ daemon required at runtime)

**Requirements:**
- `network_mode: host` in Docker (HCI sockets use `AF_BLUETOOTH`)
- `CAP_NET_ADMIN` and `CAP_NET_RAW` capabilities
- Root or equivalent capabilities for HCI device initialization

**Configuration:**
```yaml
bluetooth:
  backend: hci
  hci_device: 0   # Use hci0
```

## D-Bus Backend

The D-Bus backend communicates with the BlueZ daemon through the system D-Bus. BlueZ manages the adapter, and ruuvihome receives advertisement data via D-Bus signals.

**When to use:**
- Desktop Linux or Raspberry Pi OS where BlueZ manages Bluetooth
- Multiple applications share the Bluetooth adapter
- HCI backend fails due to adapter being claimed by BlueZ
- Systems where direct HCI access is restricted

**Requirements:**
- BlueZ daemon running on the host (`systemctl status bluetooth`)
- System D-Bus socket accessible (`/var/run/dbus`)
- In Docker, mount the D-Bus socket: `-v /var/run/dbus:/var/run/dbus:ro`

**Configuration:**
```yaml
bluetooth:
  backend: dbus
```

The D-Bus backend auto-detects the first available BlueZ adapter. The `hci_device` setting is ignored.

## Auto Backend (Default)

The `auto` backend tries HCI first. If HCI initialization fails within 5 seconds (e.g., adapter is claimed by BlueZ, permissions are insufficient, or no HCI device exists), it automatically falls back to D-Bus.

This is the recommended setting for most users.

**Configuration:**
```yaml
bluetooth:
  backend: auto
```

## Platform Compatibility

| Platform | Recommended Backend |
|----------|-------------------|
| Docker on Linux server | `hci` or `auto` |
| Raspberry Pi OS | `dbus` or `auto` |
| Ubuntu/Debian desktop | `dbus` or `auto` |
| Alpine Linux (minimal) | `hci` (no BlueZ daemon) |
| Docker on macOS/Windows | Not supported (no BLE passthrough) |

## Troubleshooting

### HCI backend fails with "no such device"
The Bluetooth adapter is not present or not recognized. Check `hciconfig` on the host.

### HCI backend fails with "permission denied"
The process lacks `CAP_NET_ADMIN` and `CAP_NET_RAW`. In Docker, ensure these capabilities are added.

### HCI backend fails with "device busy"
BlueZ has claimed the adapter. Either stop BlueZ (`sudo systemctl stop bluetooth`) or switch to the `dbus` backend.

### D-Bus backend fails with "no BlueZ adapter found"
BlueZ is not running or has no adapters. Start the Bluetooth service: `sudo systemctl start bluetooth`.

### D-Bus backend fails with "failed to connect to system D-Bus"
The system D-Bus socket is not accessible. In Docker, mount it: `-v /var/run/dbus:/var/run/dbus:ro`.

See also: [TROUBLESHOOTING.md](TROUBLESHOOTING.md)
