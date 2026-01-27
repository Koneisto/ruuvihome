package homeassistant

import (
	"context"
	"fmt"
	"strings"

	json "github.com/goccy/go-json"

	"ruuvihome/config"
	"ruuvihome/parser"
)

// Discovery handles Home Assistant MQTT Discovery
type Discovery struct {
	cfg    *config.Config
	mqtt   MQTTPublisher
	logger *config.Logger
}

// MQTTPublisher interface for publishing raw MQTT messages
type MQTTPublisher interface {
	PublishRaw(ctx context.Context, topic string, payload []byte, qos byte, retain bool) error
}

// NewDiscovery creates a new Discovery handler
func NewDiscovery(cfg *config.Config, mqtt MQTTPublisher, logger *config.Logger) *Discovery {
	return &Discovery{
		cfg:    cfg,
		mqtt:   mqtt,
		logger: logger,
	}
}

// SensorConfig represents a Home Assistant sensor discovery config
type SensorConfig struct {
	Name              string       `json:"name"`
	UniqueID          string       `json:"unique_id"`
	StateTopic        string       `json:"state_topic"`
	ValueTemplate     string       `json:"value_template"`
	UnitOfMeasurement string       `json:"unit_of_measurement,omitempty"`
	DeviceClass       string       `json:"device_class,omitempty"`
	StateClass        string       `json:"state_class,omitempty"`
	Icon              string       `json:"icon,omitempty"`
	Device            DeviceConfig `json:"device"`
}

// DeviceConfig represents the device info for HA
type DeviceConfig struct {
	Identifiers  []string `json:"identifiers"`
	Name         string   `json:"name"`
	Manufacturer string   `json:"manufacturer"`
	Model        string   `json:"model"`
}

// sensorDefinition defines a sensor type for discovery
type sensorDefinition struct {
	suffix       string
	name         string
	valueKey     string
	unit         string
	deviceClass  string
	stateClass   string
	icon         string
	precision    int  // Decimal places for HA display (European standard)
	format5Only  bool
	format6Only  bool // For Format 6 (Ruuvi Air)
	formatE1Only bool
}

// Sensor definitions with European standard precision
// Temperature: 1 decimal, Humidity: 1 decimal, Pressure: 0 (Pa)
// PM values: 1 decimal, CO2: 0 decimals, VOC/NOx: 0 decimals
var sensorDefinitions = []sensorDefinition{
	// Common sensors
	{suffix: "temperature", name: "Temperature", valueKey: "temperature", unit: "°C", deviceClass: "temperature", stateClass: "measurement", precision: 2},
	{suffix: "humidity", name: "Humidity", valueKey: "humidity", unit: "%", deviceClass: "humidity", stateClass: "measurement", precision: 2},
	{suffix: "pressure", name: "Pressure", valueKey: "pressure", unit: "Pa", deviceClass: "atmospheric_pressure", stateClass: "measurement", precision: 0},
	{suffix: "rssi", name: "Signal Strength", valueKey: "rssi", unit: "dBm", deviceClass: "signal_strength", stateClass: "measurement", precision: 0},
	{suffix: "dew_point", name: "Dew Point", valueKey: "dew_point", unit: "°C", deviceClass: "temperature", stateClass: "measurement", precision: 2},
	{suffix: "absolute_humidity", name: "Absolute Humidity", valueKey: "absolute_humidity", unit: "g/m³", stateClass: "measurement", icon: "mdi:water", precision: 2},
	{suffix: "air_density", name: "Air Density", valueKey: "air_density", unit: "kg/m³", stateClass: "measurement", icon: "mdi:air-filter", precision: 4},

	// Format 5 specific (RuuviTag Pro)
	{suffix: "battery", name: "Battery", valueKey: "battery_voltage", unit: "V", deviceClass: "voltage", stateClass: "measurement", precision: 3, format5Only: true},
	{suffix: "tx_power", name: "TX Power", valueKey: "tx_power", unit: "dBm", stateClass: "measurement", icon: "mdi:signal", precision: 0, format5Only: true},
	{suffix: "acceleration_x", name: "Acceleration X", valueKey: "acceleration_x", unit: "mG", stateClass: "measurement", icon: "mdi:axis-x-arrow", precision: 0, format5Only: true},
	{suffix: "acceleration_y", name: "Acceleration Y", valueKey: "acceleration_y", unit: "mG", stateClass: "measurement", icon: "mdi:axis-y-arrow", precision: 0, format5Only: true},
	{suffix: "acceleration_z", name: "Acceleration Z", valueKey: "acceleration_z", unit: "mG", stateClass: "measurement", icon: "mdi:axis-z-arrow", precision: 0, format5Only: true},
	{suffix: "movement_counter", name: "Movement Counter", valueKey: "movement_counter", stateClass: "total_increasing", icon: "mdi:run", precision: 0, format5Only: true},
	{suffix: "sequence", name: "Sequence", valueKey: "sequence", stateClass: "total_increasing", icon: "mdi:counter", precision: 0, format5Only: true},

	// Format 6 specific (Ruuvi Air)
	{suffix: "pm2_5", name: "PM2.5", valueKey: "pm2_5", unit: "µg/m³", deviceClass: "pm25", stateClass: "measurement", precision: 1, format6Only: true},
	{suffix: "co2", name: "CO2", valueKey: "co2", unit: "ppm", deviceClass: "carbon_dioxide", stateClass: "measurement", precision: 0, format6Only: true},
	{suffix: "voc_index", name: "VOC Index", valueKey: "voc_index", stateClass: "measurement", icon: "mdi:air-filter", precision: 0, format6Only: true},
	{suffix: "nox_index", name: "NOx Index", valueKey: "nox_index", stateClass: "measurement", icon: "mdi:molecule", precision: 0, format6Only: true},
	{suffix: "sequence_f6", name: "Sequence", valueKey: "sequence", stateClass: "total_increasing", icon: "mdi:counter", precision: 0, format6Only: true},
	{suffix: "air_quality_index_f6", name: "Air Quality Index", valueKey: "air_quality_index", stateClass: "measurement", icon: "mdi:air-purifier", precision: 0, format6Only: true},

	// Format E1 specific
	{suffix: "pm1_0", name: "PM1.0", valueKey: "pm1_0", unit: "µg/m³", deviceClass: "pm1", stateClass: "measurement", precision: 1, formatE1Only: true},
	{suffix: "pm2_5_e1", name: "PM2.5", valueKey: "pm2_5", unit: "µg/m³", deviceClass: "pm25", stateClass: "measurement", precision: 1, formatE1Only: true},
	{suffix: "pm4_0", name: "PM4.0", valueKey: "pm4_0", unit: "µg/m³", stateClass: "measurement", icon: "mdi:blur", precision: 1, formatE1Only: true},
	{suffix: "pm10_0", name: "PM10", valueKey: "pm10_0", unit: "µg/m³", deviceClass: "pm10", stateClass: "measurement", precision: 1, formatE1Only: true},
	{suffix: "co2_e1", name: "CO2", valueKey: "co2", unit: "ppm", deviceClass: "carbon_dioxide", stateClass: "measurement", precision: 0, formatE1Only: true},
	{suffix: "voc_index_e1", name: "VOC Index", valueKey: "voc_index", stateClass: "measurement", icon: "mdi:air-filter", precision: 0, formatE1Only: true},
	{suffix: "nox_index_e1", name: "NOx Index", valueKey: "nox_index", stateClass: "measurement", icon: "mdi:molecule", precision: 0, formatE1Only: true},
	{suffix: "luminosity", name: "Luminosity", valueKey: "luminosity", unit: "lx", deviceClass: "illuminance", stateClass: "measurement", precision: 1, formatE1Only: true},
	{suffix: "air_quality_index", name: "Air Quality Index", valueKey: "air_quality_index", stateClass: "measurement", icon: "mdi:air-purifier", precision: 0, formatE1Only: true},
}

// PublishDiscovery publishes Home Assistant discovery messages for a device
func (d *Discovery) PublishDiscovery(ctx context.Context, m *parser.Measurement) error {
	// Create normalized MAC for unique IDs (lowercase, no colons)
	normalizedMAC := strings.ToLower(strings.ReplaceAll(m.MAC, ":", ""))

	// Determine device name
	deviceName := m.Name
	if deviceName == "" {
		deviceName = fmt.Sprintf("Ruuvi %s", m.MAC[len(m.MAC)-5:])
	}

	// Determine model based on format
	model := "RuuviTag"
	if m.Format == parser.Format6 || m.Format == parser.FormatE1 {
		model = "Ruuvi Air"
	}

	// Base device config
	device := DeviceConfig{
		Identifiers:  []string{fmt.Sprintf("ruuvi_%s", normalizedMAC)},
		Name:         deviceName,
		Manufacturer: "Ruuvi",
		Model:        model,
	}

	// State topic
	stateTopic := fmt.Sprintf("%s/%s", d.cfg.MQTT.TopicPrefix, m.MAC)

	// Publish discovery for each sensor
	for _, sensor := range sensorDefinitions {
		// Skip format-specific sensors that don't apply
		if sensor.format5Only && m.Format != parser.Format5 {
			continue
		}
		if sensor.format6Only && m.Format != parser.Format6 {
			continue
		}
		if sensor.formatE1Only && m.Format != parser.FormatE1 {
			continue
		}

		// Build discovery topic
		discoveryTopic := fmt.Sprintf("%s/sensor/ruuvi_%s_%s/config",
			d.cfg.HomeAssistant.DiscoveryPrefix, normalizedMAC, sensor.suffix)

		// Build value template with rounding for European display standards
		valueTemplate := fmt.Sprintf("{{ value_json.%s | round(%d) }}", sensor.valueKey, sensor.precision)

		// Build sensor config
		sensorCfg := SensorConfig{
			Name:              fmt.Sprintf("%s %s", deviceName, sensor.name),
			UniqueID:          fmt.Sprintf("ruuvi_%s_%s", normalizedMAC, sensor.suffix),
			StateTopic:        stateTopic,
			ValueTemplate:     valueTemplate,
			UnitOfMeasurement: sensor.unit,
			DeviceClass:       sensor.deviceClass,
			StateClass:        sensor.stateClass,
			Icon:              sensor.icon,
			Device:            device,
		}

		// Serialize config
		payload, err := json.Marshal(sensorCfg)
		if err != nil {
			return fmt.Errorf("failed to serialize discovery config: %w", err)
		}

		// Publish with retain
		if err := d.mqtt.PublishRaw(ctx, discoveryTopic, payload, 1, true); err != nil {
			return fmt.Errorf("failed to publish discovery for %s: %w", sensor.suffix, err)
		}

		d.logger.Debug("Published HA discovery",
			"sensor", sensor.suffix,
			"topic", discoveryTopic)
	}

	return nil
}
