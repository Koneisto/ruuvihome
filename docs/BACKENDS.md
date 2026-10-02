# BLE Scanning Modes

ruuvihome receives Ruuvi advertisements in one of two modes, selected with `bluetooth.mode`.

| | dbus (default) | hci |
|---|---|---|
| How | BlueZ discovery over D-Bus (godbus) | Direct HCI user channel (`AF_BLUETOOTH`, `HCI_CHANNEL_USER`) |
| Adapter | Shared with BlueZ | Exclusive to ruuvihome |
| Scan restarts | Every 10.24 s (BlueZ / kernel discovery timeout) | None: enabled once |
| Extended advertising (Ruuvi Air E1) | When the kernel uses extended scanning | Bluetooth 5 adapters: yes (LE 1M + LE Coded) |
| Needs | `/var/run/dbus` mount, BlueZ running | `network_mode: host`, `CAP_NET_ADMIN` |
| `hci_device`, `scan_window`, `scan_interval` | Ignored | Used |

## dbus mode

ruuvihome sets a discovery filter (LE, duplicate data) and starts BlueZ discovery. Advertisements arrive as
`PropertiesChanged` signals on the device objects. With `filter_mode: named` only the configured tags are subscribed, so
other nearby BLE devices do not wake the process.

BlueZ discovery is not continuous at the controller level: the kernel ends LE discovery after 10.24 s and bluetoothd
starts it again, so the controller receives LE Set Scan Enable (disable and enable) about 8600 times a day. Most
controllers handle this fine.

```yaml
bluetooth:
  mode: dbus
```

## hci mode

ruuvihome brings the adapter down (if BlueZ powered it up), binds it with the HCI user channel and initialises it:

1. HCI Reset, Set Event Mask
2. LE Read Local Supported Features
3. Bluetooth 5 (LE Extended Advertising): LE Set Extended Scan Parameters (passive, LE 1M and LE Coded if supported) and
   LE Set Extended Scan Enable; otherwise LE Set Scan Parameters and LE Set Scan Enable
4. Scanning stays on. Legacy and extended advertising reports are parsed, fragmented extended data is reassembled.

If no Ruuvi advertisement arrives for 60 s, or the adapter disappears (USB reset), the session ends and the controller
is initialised again after 5 s.

```yaml
bluetooth:
  mode: hci
  hci_device: 0
```

Use hci mode when the adapter is dedicated to ruuvihome, and especially when the kernel log shows
`hci0: command 0x200c tx timeout` or `Unable to disable scanning` in dbus mode: some controllers (for example the
Broadcom BCM20702 in the ASUS USB-BT400) hang on the repeated scan restarts. While ruuvihome runs, `bluetoothctl` does not
see the adapter.

Tested controllers: Broadcom BCM20702 (Bluetooth 4.0, legacy scanning), Realtek RTL8761B (ASUS USB-BT500) and MediaTek
MT7925 (Bluetooth 5, extended + coded).

## Backwards compatibility

v1.1.0 used `bluetooth.backend` (`auto`, `hci`, `dbus`) with a go-ble based HCI backend. When `mode` is not set,
`backend: hci` selects hci mode and `auto`/`dbus` select dbus mode. The go-ble backend has been removed.

## Troubleshooting

- **"bind hci0 user channel: ... busy"**: another program keeps the adapter open. ruuvihome retries; stop the other
  program or use dbus mode.
- **"socket(AF_BLUETOOTH): address family not supported"**: the container is not in the host network namespace
  (`network_mode: host`).
- **"operation not permitted"**: `CAP_NET_ADMIN` is missing.
- **No E1 frames in dbus mode**: use hci mode with a Bluetooth 5 adapter.
- **"no BlueZ adapter found" (dbus mode)**: check `bluetoothctl list`; another program (or hci mode) may hold the adapter.
