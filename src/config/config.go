package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents the application configuration
type Config struct {
	Bluetooth     BluetoothConfig          `yaml:"bluetooth"`
	MQTT          MQTTConfig               `yaml:"mqtt"`
	Processing    ProcessingConfig         `yaml:"processing"`
	HomeAssistant HomeAssistantConfig      `yaml:"homeassistant"`
	Devices       map[string]*DeviceConfig `yaml:"devices"`
	Logging       LoggingConfig            `yaml:"logging"`
}

// BluetoothConfig contains BLE scanner settings
type BluetoothConfig struct {
	HCIDevice    int           `yaml:"hci_device"`
	ScanWindow   time.Duration `yaml:"scan_window"`
	ScanInterval time.Duration `yaml:"scan_interval"`
}

// MQTTConfig contains MQTT broker settings
type MQTTConfig struct {
	Broker      string `yaml:"broker"`
	ClientID    string `yaml:"client_id"`
	TopicPrefix string `yaml:"topic_prefix"`
	QoS         byte   `yaml:"qos"`
	Retain      bool   `yaml:"retain"`
	Username    string `yaml:"username"`
	Password    string `yaml:"password" json:"-"` // Never serialize password
	CACert      string `yaml:"ca_cert"`
}

// ProcessingConfig contains data processing settings
type ProcessingConfig struct {
	MinInterval    time.Duration `yaml:"min_interval"`
	ExtendedValues bool          `yaml:"extended_values"`
	FilterMode     string        `yaml:"filter_mode"` // "named" or "all"
}

// HomeAssistantConfig contains HA MQTT Discovery settings
type HomeAssistantConfig struct {
	Discovery       bool   `yaml:"discovery"`
	DiscoveryPrefix string `yaml:"discovery_prefix"`
}

// DeviceConfig contains per-device settings
type DeviceConfig struct {
	Name        string        `yaml:"name"`
	MinInterval time.Duration `yaml:"min_interval,omitempty"`
}

// LoggingConfig contains logging settings
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"` // "simple" or "json"
}

// Load reads and parses the configuration file
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := &Config{
		// Set defaults
		Bluetooth: BluetoothConfig{
			HCIDevice:    0,
			ScanWindow:   10 * time.Millisecond,
			ScanInterval: 10 * time.Millisecond,
		},
		MQTT: MQTTConfig{
			Broker:      "tcp://127.0.0.1:1883",
			ClientID:    "ruuvihome",
			TopicPrefix: "ruuvi",
			QoS:         0,
			Retain:      false,
		},
		Processing: ProcessingConfig{
			MinInterval:    60 * time.Second,
			ExtendedValues: true,
			FilterMode:     "named",
		},
		HomeAssistant: HomeAssistantConfig{
			Discovery:       false,
			DiscoveryPrefix: "homeassistant",
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "simple",
		},
		Devices: make(map[string]*DeviceConfig),
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return cfg, nil
}

// Validate checks the configuration for errors
func (c *Config) Validate() error {
	// Validate broker URL
	if c.MQTT.Broker == "" {
		return errors.New("mqtt.broker is required")
	}

	// Check for path traversal in broker URL
	if strings.Contains(c.MQTT.Broker, "..") {
		return errors.New("invalid broker URL: path traversal detected")
	}

	// Validate broker scheme
	validSchemes := []string{"tcp://", "ssl://", "tls://", "ws://", "wss://"}
	hasValidScheme := false
	for _, scheme := range validSchemes {
		if strings.HasPrefix(c.MQTT.Broker, scheme) {
			hasValidScheme = true
			break
		}
	}
	if !hasValidScheme {
		return fmt.Errorf("invalid broker URL scheme: must start with tcp://, ssl://, tls://, ws://, or wss://")
	}

	// Validate filter mode
	if c.Processing.FilterMode != "named" && c.Processing.FilterMode != "all" {
		return fmt.Errorf("invalid filter_mode: must be 'named' or 'all'")
	}

	// Validate QoS
	if c.MQTT.QoS > 2 {
		return fmt.Errorf("invalid QoS: must be 0, 1, or 2")
	}

	// Validate logging level
	validLevels := []string{"trace", "debug", "info", "warn", "error"}
	levelValid := false
	for _, l := range validLevels {
		if c.Logging.Level == l {
			levelValid = true
			break
		}
	}
	if !levelValid {
		return fmt.Errorf("invalid logging level: must be trace, debug, info, warn, or error")
	}

	// Warn about unencrypted MQTT (not an error, just a warning)
	// This is handled in the logger after config is loaded

	return nil
}

// Logger provides structured logging
type Logger struct {
	level  int
	format string
}

const (
	levelTrace = 0
	levelDebug = 1
	levelInfo  = 2
	levelWarn  = 3
	levelError = 4
)

// NewLogger creates a new logger instance
func NewLogger(level, format string) *Logger {
	l := &Logger{format: format}

	switch level {
	case "trace":
		l.level = levelTrace
	case "debug":
		l.level = levelDebug
	case "info":
		l.level = levelInfo
	case "warn":
		l.level = levelWarn
	case "error":
		l.level = levelError
	default:
		l.level = levelInfo
	}

	return l
}

func (l *Logger) log(level int, levelStr, msg string, keyvals ...interface{}) {
	if level < l.level {
		return
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05")

	if l.format == "json" {
		// JSON format
		fmt.Printf(`{"time":"%s","level":"%s","msg":"%s"`, timestamp, levelStr, msg)
		for i := 0; i < len(keyvals); i += 2 {
			if i+1 < len(keyvals) {
				fmt.Printf(`,"%v":"%v"`, keyvals[i], keyvals[i+1])
			}
		}
		fmt.Println("}")
	} else {
		// Simple format
		fmt.Printf("%s [%s] %s", timestamp, levelStr, msg)
		for i := 0; i < len(keyvals); i += 2 {
			if i+1 < len(keyvals) {
				fmt.Printf(" %v=%v", keyvals[i], keyvals[i+1])
			}
		}
		fmt.Println()
	}
}

// Trace logs a trace message
func (l *Logger) Trace(msg string, keyvals ...interface{}) {
	l.log(levelTrace, "TRACE", msg, keyvals...)
}

// Debug logs a debug message
func (l *Logger) Debug(msg string, keyvals ...interface{}) {
	l.log(levelDebug, "DEBUG", msg, keyvals...)
}

// Info logs an info message
func (l *Logger) Info(msg string, keyvals ...interface{}) {
	l.log(levelInfo, "INFO", msg, keyvals...)
}

// Warn logs a warning message
func (l *Logger) Warn(msg string, keyvals ...interface{}) {
	l.log(levelWarn, "WARN", msg, keyvals...)
}

// Error logs an error message
func (l *Logger) Error(msg string, keyvals ...interface{}) {
	l.log(levelError, "ERROR", msg, keyvals...)
}
