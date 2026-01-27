package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"ruuvihome/ble"
	"ruuvihome/config"
	"ruuvihome/homeassistant"
	"ruuvihome/mqtt"
	"ruuvihome/parser"
)

var (
	configPath = flag.String("config", "/config.yml", "Path to configuration file")
	version    = "1.0.0"
)

// macCache caches sanitized MAC addresses to avoid repeated string allocations
var macCache sync.Map

func main() {
	flag.Parse()

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid configuration: %v\n", err)
		os.Exit(1)
	}

	// Setup logging
	logger := config.NewLogger(cfg.Logging.Level, cfg.Logging.Format)
	logger.Info("Starting ruuvihome", "version", version)

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		logger.Info("Shutdown signal received", "signal", sig.String())
		cancel()
	}()

	// Run main application
	if err := run(ctx, cfg, logger); err != nil && err != context.Canceled {
		logger.Error("Fatal error", "error", err)
		os.Exit(1)
	}

	logger.Info("Shutdown complete")
}

func run(ctx context.Context, cfg *config.Config, logger *config.Logger) error {
	// Create MQTT client
	mqttClient, err := mqtt.NewClient(cfg, logger)
	if err != nil {
		return fmt.Errorf("failed to create MQTT client: %w", err)
	}

	// Connect to MQTT broker
	if err := mqttClient.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to MQTT broker: %w", err)
	}
	defer mqttClient.Disconnect()

	logger.Info("Connected to MQTT broker", "broker", cfg.MQTT.Broker)

	// Create Home Assistant discovery handler
	var haDiscovery *homeassistant.Discovery
	if cfg.HomeAssistant.Discovery {
		haDiscovery = homeassistant.NewDiscovery(cfg, mqttClient, logger)
		logger.Info("Home Assistant MQTT Discovery enabled", "prefix", cfg.HomeAssistant.DiscoveryPrefix)
	}

	// Create rate limiter for publishing
	rateLimiter := mqtt.NewRateLimiter(cfg.Processing.MinInterval, cfg)

	// Create parser
	p := parser.New(cfg.Processing.ExtendedValues, logger)

	// Track discovered devices for HA discovery
	var discoveredDevices sync.Map

	// Create BLE scanner with advertisement handler
	scanner, err := ble.NewScanner(cfg, logger, func(addr string, rssi int, manufacturerData []byte) {
		// Normalize MAC to uppercase (go-ble returns lowercase)
		addr = strings.ToUpper(addr)

		// Get or create cached sanitized MAC (avoids repeated allocations)
		sanitized := getSanitizedMAC(addr)

		// Check if device is in allowed list FIRST (early exit for filtered devices)
		deviceConfig, allowed := cfg.Devices[addr]
		if cfg.Processing.FilterMode == "named" && !allowed {
			logger.Debug("Device not in allowed list", "mac", sanitized)
			return
		}

		// Check rate limit BEFORE parsing (avoid unnecessary work)
		if !rateLimiter.Allow(addr) {
			logger.Debug("Rate limited", "mac", sanitized)
			return
		}

		// Parse the advertisement data
		measurement, err := p.Parse(manufacturerData)
		if err != nil {
			logger.Debug("Failed to parse advertisement", "mac", sanitized, "error", err)
			return
		}

		// Set MAC and RSSI from advertisement
		measurement.MAC = addr
		measurement.RSSI = rssi
		measurement.Timestamp = time.Now().Unix()

		// Set device name if configured
		if deviceConfig != nil && deviceConfig.Name != "" {
			measurement.Name = deviceConfig.Name
		}

		// Publish Home Assistant discovery (once per device)
		if haDiscovery != nil {
			if _, discovered := discoveredDevices.Load(addr); !discovered {
				if err := haDiscovery.PublishDiscovery(ctx, measurement); err != nil {
					logger.Error("Failed to publish HA discovery", "mac", sanitized, "error", err)
				} else {
					discoveredDevices.Store(addr, true)
					logger.Info("Published HA discovery", "mac", sanitized, "name", measurement.Name)
				}
			}
		}

		// Publish measurement to MQTT
		if err := mqttClient.Publish(ctx, measurement); err != nil {
			logger.Error("Failed to publish measurement", "mac", sanitized, "error", err)
			return
		}

		logger.Debug("Published measurement",
			"mac", sanitized,
			"name", measurement.Name,
			"temp", measurement.Temperature,
			"humidity", measurement.Humidity,
			"format", measurement.Format)
	})
	if err != nil {
		return fmt.Errorf("failed to create BLE scanner: %w", err)
	}

	// Start scanning
	logger.Info("Starting BLE scan", "hci_device", cfg.Bluetooth.HCIDevice)

	if err := scanner.Start(ctx); err != nil {
		return fmt.Errorf("BLE scan failed: %w", err)
	}

	return nil
}

// getSanitizedMAC returns a cached partially masked MAC address for logging
func getSanitizedMAC(mac string) string {
	if cached, ok := macCache.Load(mac); ok {
		return cached.(string)
	}

	var sanitized string
	if len(mac) < 8 {
		sanitized = "XX:XX:XX:XX:XX:XX"
	} else {
		sanitized = "XX:XX:XX:XX:" + mac[len(mac)-5:]
	}

	macCache.Store(mac, sanitized)
	return sanitized
}
