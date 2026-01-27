# ruuvihome

Lightweight BLE-to-MQTT bridge for Ruuvi sensors with Home Assistant auto-discovery.

## Features

- **Direct BLE to MQTT** - No intermediate services or cloud dependencies
- **Home Assistant MQTT Discovery** - Sensors appear automatically with proper device grouping
- **Multiple Sensor Formats** - Supports RuuviTag (Format 5/E1) and Ruuvi Air (Format 6/E1)
- **Calculated Values** - Dew point, absolute humidity, air density, and Air Quality Index
- **Per-Device Rate Limiting** - Control publish frequency to avoid flooding MQTT
- **Tiny Footprint** - ~1.9MB Docker image, ~10MB RAM usage

## Quick Start

```bash
git clone https://github.com/koneisto/ruuvihome.git
cd ruuvihome
cp config.example.yml config.yml
# Edit config.yml - add your Ruuvi MAC addresses
docker compose up -d
```

## Why ruuvihome?

| Feature | Benefit |
|---------|---------|
| Single container | Simple and lightweight, nothing else needed |
| Direct BLE to MQTT | No intermediate services, minimal latency |
| HA Auto-Discovery | Sensors appear in Home Assistant automatically |
| Tiny footprint | 1.9MB image, ~10MB RAM |
| Full precision MQTT | Raw values available for custom processing |
| Rounded HA values | European display standards in Home Assistant |

## Configuration

Copy `config.example.yml` to `config.yml` and configure:

### Essential Settings

```yaml
mqtt:
  broker: tcp://192.168.1.100:1883  # Your MQTT broker
  topic_prefix: ruuvi               # Topics: ruuvi/<MAC>

devices:
  AA:BB:CC:DD:EE:FF:                # Your Ruuvi MAC address
    name: living_room               # Friendly name for HA
```

### Finding Your Device MAC Addresses

1. **Ruuvi Station App**: Settings > See Raw Sensor Data
2. **Linux command**: `sudo hcitool lescan | grep Ruuvi`
3. **Check logs**: Set `filter_mode: all` temporarily and watch the logs

### Device Filtering

```yaml
processing:
  filter_mode: named   # Only process devices listed in 'devices:' section
  # filter_mode: all   # Process ALL detected Ruuvi devices (use with caution)
```

### Rate Limiting

```yaml
processing:
  min_interval: 60s    # Global: publish at most once per 60 seconds per device

devices:
  AA:BB:CC:DD:EE:FF:
    name: living_room
    min_interval: 30s  # Override: this device publishes every 30 seconds
```

### MQTT Authentication

```yaml
mqtt:
  broker: ssl://mqtt.example.com:8883
  username: ruuvihome
  password: your_secure_password
  ca_cert: /path/to/ca.crt  # For self-signed certificates
```

## Supported Devices & Data

### Format 5 - RuuviTag (Pro)

Standard RuuviTag sensors broadcasting Format 5 (RAWv2):

| Field | Unit | Description |
|-------|------|-------------|
| temperature | °C | -40 to +85 °C, 0.005° resolution |
| humidity | % | 0-100%, 0.0025% resolution |
| pressure | Pa | Atmospheric pressure |
| acceleration_x/y/z | mG | Movement in 3 axes |
| battery_voltage | V | Battery level |
| tx_power | dBm | Transmit power |
| movement_counter | - | Increments on movement |
| sequence | - | Packet sequence number |

### Format 6 - Ruuvi Air

Ruuvi Air sensors with air quality measurements:

| Field | Unit | Description |
|-------|------|-------------|
| temperature | °C | Ambient temperature |
| humidity | % | Relative humidity |
| pressure | Pa | Atmospheric pressure |
| pm2_5 | µg/m³ | PM2.5 particulate matter |
| co2 | ppm | Carbon dioxide |
| voc_index | 1-500 | Volatile organic compounds index |
| nox_index | 1-500 | Nitrogen oxides index |

### Format E1

> **Note:** Format E1 is not yet supported. Waiting for Go BLE libraries to support Bluetooth 5 extended advertising.

Alternative data format with additional sensors:

| Field | Unit | Description |
|-------|------|-------------|
| pm1_0, pm2_5, pm4_0, pm10_0 | µg/m³ | Full PM spectrum |
| co2 | ppm | Carbon dioxide |
| voc_index, nox_index | 1-500 | Gas indices |
| luminosity | lux | Light level |

### Calculated Values

When `extended_values: true` (default):

| Field | Unit | Description |
|-------|------|-------------|
| dew_point | °C | Temperature at which condensation occurs |
| absolute_humidity | g/m³ | Water vapor mass per volume |
| air_density | kg/m³ | Calculated from temp, humidity, pressure |
| air_quality_index | 0-500 | Combined AQI (Ruuvi Air only) |

## MQTT Output

Measurements are published to `<topic_prefix>/<MAC>`:

```
Topic: ruuvi/AA:BB:CC:DD:EE:FF
```

### Example Payload (RuuviTag)

```json
{
  "mac": "AA:BB:CC:DD:EE:FF",
  "name": "living_room",
  "timestamp": 1704067200,
  "format": 5,
  "rssi": -65,
  "temperature": 21.345,
  "humidity": 45.6725,
  "pressure": 101325,
  "acceleration_x": 12,
  "acceleration_y": -8,
  "acceleration_z": 1024,
  "battery_voltage": 2.945,
  "tx_power": 4,
  "movement_counter": 42,
  "sequence": 12345,
  "dew_point": 9.234,
  "absolute_humidity": 8.456,
  "air_density": 1.1923
}
```

### Example Payload (Ruuvi Air)

```json
{
  "mac": "11:22:33:44:55:66",
  "name": "bedroom",
  "timestamp": 1704067200,
  "format": 6,
  "rssi": -58,
  "temperature": 22.105,
  "humidity": 42.8375,
  "pressure": 101250,
  "pm2_5": 5.2,
  "co2": 623,
  "voc_index": 95,
  "nox_index": 12,
  "dew_point": 8.456,
  "absolute_humidity": 7.89,
  "air_density": 1.1897,
  "air_quality_index": 48
}
```

## Home Assistant Integration

### Automatic Discovery

When `homeassistant.discovery: true`, sensors are automatically created in Home Assistant via MQTT Discovery. Each Ruuvi device appears as a device with multiple sensor entities.

**Discovery topics:**
```
homeassistant/sensor/ruuvi_aabbccddeeff_temperature/config
homeassistant/sensor/ruuvi_aabbccddeeff_humidity/config
...
```

### Sensor Entities Created

For a RuuviTag named "living_room", you'll see:
- `sensor.living_room_temperature`
- `sensor.living_room_humidity`
- `sensor.living_room_pressure`
- `sensor.living_room_battery`
- `sensor.living_room_dew_point`
- ... and more

For Ruuvi Air, additional entities:
- `sensor.bedroom_pm2_5`
- `sensor.bedroom_co2`
- `sensor.bedroom_voc_index`
- `sensor.bedroom_air_quality_index`

### Display Precision

Home Assistant templates apply European standard rounding:
- Temperature: 2 decimal places (21.35 °C)
- Humidity: 2 decimal places (45.67 %)
- Pressure: whole numbers (101325 Pa)
- PM values: 1 decimal place (5.2 µg/m³)

Raw MQTT values retain full precision for custom automations.

### Manual Configuration

If you prefer manual MQTT sensors instead of auto-discovery:

```yaml
mqtt:
  sensor:
    - name: "Living Room Temperature"
      state_topic: "ruuvi/AA:BB:CC:DD:EE:FF"
      value_template: "{{ value_json.temperature | round(1) }}"
      unit_of_measurement: "°C"
      device_class: temperature
```

## Architecture

### Why Host Networking?

ruuvihome requires `network_mode: host` because Linux Bluetooth (HCI) sockets operate at the kernel level using `AF_BLUETOOTH`. Docker's network namespacing cannot bridge raw HCI sockets.

This is a Linux kernel limitation, not a software design choice.

### Data Flow

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│  Ruuvi Tag  │────>│  BLE/HCI    │────>│  ruuvihome  │────>│    MQTT     │
│  (BLE Adv)  │     │  (hci0)     │     │  (Parser)   │     │   Broker    │
└─────────────┘     └─────────────┘     └─────────────┘     └─────────────┘
                                               │
                                               v
                                        ┌─────────────┐
                                        │ Home Asst.  │
                                        │ (Discovery) │
                                        └─────────────┘
```

### Security Hardening

The Docker container runs with minimal privileges:

```yaml
cap_drop:
  - ALL
cap_add:
  - NET_ADMIN     # Required for BLE HCI operations
  - NET_RAW       # Required for raw socket access
security_opt:
  - no-new-privileges:true
read_only: true
```

The only capabilities granted are those strictly required for Bluetooth operations.

## Building from Source

### Requirements

- Go 1.22 or later
- Linux with BlueZ (for BLE support)
- Docker (optional, for containerized builds)

### Local Build

```bash
cd src
go mod download
CGO_ENABLED=1 go build -o ruuvihome .
```

### Docker Build

```bash
docker compose build
```

The Dockerfile uses a multi-stage build:
1. Builds with Go 1.22 Alpine
2. Compresses with UPX (~60% size reduction)
3. Runs from scratch (no OS layer)

## Troubleshooting

### No devices detected

1. Check Bluetooth adapter: `hciconfig`
2. Verify adapter is up: `sudo hciconfig hci0 up`
3. Test scanning: `sudo hcitool lescan`
4. Check container has BLE access: ensure `network_mode: host`

### Permission denied

BLE HCI requires root or `CAP_NET_ADMIN` + `CAP_NET_RAW`. The Docker container handles this, but for local runs:

```bash
sudo setcap 'cap_net_raw,cap_net_admin+eip' ./ruuvihome
```

### Device not publishing

1. Check MAC address format: `AA:BB:CC:DD:EE:FF:` (with trailing colon in config)
2. Verify `filter_mode: named` and device is listed
3. Check rate limiter: `min_interval` may be blocking rapid publishes
4. Enable debug logging: `logging.level: debug`

### MQTT connection failed

1. Verify broker address and port
2. Check firewall rules
3. Test with `mosquitto_sub -h <broker> -t ruuvi/#`
4. For TLS, verify certificate paths

### Home Assistant sensors not appearing

1. Verify MQTT integration is configured in HA
2. Check discovery prefix matches: default is `homeassistant`
3. Look for discovery messages: `mosquitto_sub -h <broker> -t homeassistant/sensor/#`
4. Restart HA MQTT integration if needed

## Acknowledgments

Special thanks to [Scrin/RuuviBridge](https://github.com/Scrin/RuuviBridge) - the original Ruuvi-to-MQTT bridge that inspired this project and served reliably for years.

This project builds upon:

- [go-ble/ble](https://github.com/go-ble/ble) - Pure Go Bluetooth Low Energy library
- [Ruuvi](https://ruuvi.com/) - Sensor hardware and [data format documentation](https://docs.ruuvi.com/)
- [Eclipse Paho](https://github.com/eclipse/paho.mqtt.golang) - MQTT client library
- [goccy/go-json](https://github.com/goccy/go-json) - High-performance JSON encoder

## License

MIT License - see [LICENSE](LICENSE) file.
