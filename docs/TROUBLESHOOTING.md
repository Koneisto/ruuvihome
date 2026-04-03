# Troubleshooting

Common issues and their solutions when running ruuvihome.

## BLE / Bluetooth Issues

### No devices detected

1. **Check the Bluetooth adapter is present:**
   ```bash
   hciconfig
   ```
   You should see `hci0` (or similar) listed. If not, the adapter is missing or not recognized.

2. **Verify the adapter is up:**
   ```bash
   sudo hciconfig hci0 up
   ```

3. **Test scanning manually:**
   ```bash
   sudo hcitool lescan
   ```
   You should see Ruuvi devices broadcasting. Press Ctrl+C to stop.

4. **Check Docker has BLE access:**
   - Ensure `network_mode: host` is set in docker-compose.yml
   - Ensure `CAP_NET_ADMIN` and `CAP_NET_RAW` are added

5. **Check the backend:**
   - Set `logging.level: debug` and look for backend selection messages
   - Try explicitly setting `bluetooth.backend: dbus` or `bluetooth.backend: hci`

### Permission denied (HCI backend)

BLE HCI requires root or specific Linux capabilities.

**Docker:** The docker-compose.yml already includes the required capabilities. Verify they are present:
```yaml
cap_add:
  - NET_ADMIN
  - NET_RAW
```

**Running directly on Linux:**
```bash
sudo setcap 'cap_net_raw,cap_net_admin+eip' ./ruuvihome
```

### Device busy (HCI backend)

If the HCI backend reports "device busy", another process (usually BlueZ) has claimed the adapter.

**Option A:** Stop BlueZ and use HCI backend:
```bash
sudo systemctl stop bluetooth
```

**Option B:** Switch to D-Bus backend (recommended, lets BlueZ keep managing the adapter):
```yaml
bluetooth:
  backend: dbus
```

### D-Bus connection failed

If the D-Bus backend cannot connect to the system bus:

1. **Check BlueZ is running:**
   ```bash
   systemctl status bluetooth
   ```

2. **In Docker, mount the D-Bus socket:**
   ```yaml
   volumes:
     - /var/run/dbus:/var/run/dbus:ro
   ```

3. **Check D-Bus socket permissions:**
   ```bash
   ls -la /var/run/dbus/system_bus_socket
   ```

### No BlueZ adapter found (D-Bus backend)

BlueZ sees no Bluetooth adapters.

1. **Check if the adapter is recognized by the kernel:**
   ```bash
   dmesg | grep -i bluetooth
   ```

2. **Restart BlueZ:**
   ```bash
   sudo systemctl restart bluetooth
   ```

3. **List BlueZ adapters:**
   ```bash
   bluetoothctl list
   ```

## MQTT Issues

### Connection failed

1. **Verify broker address and port:**
   ```bash
   mosquitto_sub -h <broker_ip> -p 1883 -t '#'
   ```

2. **Check firewall rules** (port 1883 for TCP, 8883 for TLS)

3. **For TLS connections:**
   - Verify the CA certificate path is correct and readable
   - Ensure the certificate is valid for the broker hostname

4. **For authenticated brokers:**
   - Double-check username and password in config.yml
   - Test with mosquitto_sub: `mosquitto_sub -h <broker> -u <user> -P <pass> -t '#'`

### Messages not appearing

1. **Subscribe to the topic and watch:**
   ```bash
   mosquitto_sub -h <broker_ip> -t 'ruuvi/#' -v
   ```

2. **Check the topic prefix** matches what you are subscribing to

3. **Check rate limiting:**
   - The default `min_interval: 60s` means at most one message per minute per device
   - Set `logging.level: debug` to see rate-limited messages

## Home Assistant Issues

### Sensors not appearing

1. **Verify MQTT integration is configured in Home Assistant**

2. **Check the discovery prefix:**
   ```yaml
   homeassistant:
     discovery: true
     discovery_prefix: homeassistant  # Must match HA MQTT config
   ```

3. **Check for discovery messages:**
   ```bash
   mosquitto_sub -h <broker_ip> -t 'homeassistant/sensor/#' -v
   ```

4. **Restart the MQTT integration** in Home Assistant (Settings > Devices & Services > MQTT > Reconfigure)

### Sensors disappear after restart

This is normal. MQTT Discovery messages are published once when a device is first seen. After restarting ruuvihome, sensors reappear once the first advertisement is received from each device.

If you want sensors to persist across restarts without waiting for new data, set `mqtt.retain: true`.

## Device Configuration Issues

### Device not publishing

1. **Check MAC address format:** Must include trailing colon in config:
   ```yaml
   devices:
     AA:BB:CC:DD:EE:FF:   # Note the trailing colon
       name: living_room
   ```

2. **Check filter mode:**
   - `filter_mode: named` only publishes devices listed under `devices:`
   - Set `filter_mode: all` temporarily to see all detected devices

3. **Check rate limiter:** The `min_interval` may be suppressing rapid publishes.
   Set `logging.level: debug` to see "Rate limited" messages.

4. **Verify the device is broadcasting:**
   ```bash
   sudo hcitool lescan | grep -i <partial_mac>
   ```

## Docker Issues

### Container keeps restarting

Check the container logs:
```bash
docker logs ruuvihome
```

Common causes:
- Config file not mounted or not found
- MQTT broker unreachable
- Bluetooth adapter not available

### High memory usage

The default memory limit is 64MB. Normal usage is around 10MB. If memory usage is high:
- Ensure `filter_mode: named` to ignore non-Ruuvi devices
- Check that the MQTT broker is reachable (failed publishes may queue up)

## Logging

Enable verbose logging to diagnose issues:

```yaml
logging:
  level: trace   # Most verbose: shows every BLE packet
  # level: debug # Shows MQTT publishes and rate limiting
  # level: info  # Default: shows startup and errors
```

Use JSON format for structured log analysis:
```yaml
logging:
  format: json
```
