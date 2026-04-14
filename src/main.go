package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"ruuvihome/ble"
	"ruuvihome/config"
	"ruuvihome/homeassistant"
	"ruuvihome/mqtt"
	"ruuvihome/parser"
)

var (
	configPath  = flag.String("config", "/config.yml", "Path to configuration file")
	healthcheck = flag.Bool("healthcheck", false, "Run health check and exit")
	version     = "1.1.0"
)

const (
	healthPort       = 8098
	watchdogTimeout  = 120 * time.Second
	watchdogInterval = 30 * time.Second
	publishQueueSize = 16
)

// macCache caches sanitized MAC addresses to avoid repeated string allocations
var macCache sync.Map

// lastPublishTime tracks the last successful MQTT publish (unix milliseconds)
var lastPublishTime atomic.Int64

func main() {
	flag.Parse()

	// Health check client mode: probe the running instance and exit
	if *healthcheck {
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", healthPort))
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		os.Exit(0)
	}

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

// publishJob is sent through the async publish channel
type publishJob struct {
	measurement *parser.Measurement
	sanitized   string
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

	// Async publish channel — decouples BLE callback from MQTT I/O
	publishCh := make(chan publishJob, publishQueueSize)

	// Publisher goroutine — handles all MQTT publishes off the BLE thread
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case job := <-publishCh:
				if err := mqttClient.Publish(ctx, job.measurement); err != nil {
					logger.Error("Failed to publish", "mac", job.sanitized, "error", err)
				} else {
					lastPublishTime.Store(time.Now().UnixMilli())
					logger.Info("Published measurement",
						"mac", job.sanitized,
						"name", job.measurement.Name)
				}
			}
		}
	}()

	// Create BLE scanner with non-blocking advertisement handler
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

		// Publish Home Assistant discovery (once per device, kept synchronous)
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

		// Non-blocking send to async publish channel
		select {
		case publishCh <- publishJob{measurement: measurement, sanitized: sanitized}:
			logger.Debug("Queued measurement",
				"mac", sanitized,
				"name", measurement.Name,
				"temp", measurement.Temperature,
				"humidity", measurement.Humidity,
				"format", measurement.Format)
		default:
			logger.Warn("Publish queue full, dropping measurement", "mac", sanitized)
		}
	})
	if err != nil {
		return fmt.Errorf("failed to create BLE scanner: %w", err)
	}

	// Start health server
	go startHealthServer(logger, scanner, cfg.Processing.MinInterval)

	// Start watchdog — exits process if BLE scanner hangs (Docker restarts it)
	go func() {
		ticker := time.NewTicker(watchdogInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				last := scanner.LastAdvTime.Load()
				if last > 0 && time.Since(time.UnixMilli(last)) > watchdogTimeout {
					logger.Error("Watchdog: no BLE advertisements for >120s, exiting for restart")
					os.Exit(1)
				}
			}
		}
	}()

	// Start scanning
	logger.Info("Starting BLE scan", "hci_device", cfg.Bluetooth.HCIDevice)

	if err := scanner.Start(ctx); err != nil {
		return fmt.Errorf("BLE scan failed: %w", err)
	}

	return nil
}

// startHealthServer runs an HTTP health endpoint for Docker HEALTHCHECK
func startHealthServer(logger *config.Logger, scanner *ble.Scanner, minInterval time.Duration) {
	threshold := 2 * minInterval
	if threshold < watchdogTimeout {
		threshold = watchdogTimeout
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		lastAdv := scanner.LastAdvTime.Load()
		lastPub := lastPublishTime.Load()

		advAge := time.Since(time.UnixMilli(lastAdv))

		healthy := lastAdv > 0 && advAge < threshold

		w.Header().Set("Content-Type", "application/json")
		if healthy {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}

		var pubAgo string
		if lastPub > 0 {
			pubAgo = time.Since(time.UnixMilli(lastPub)).Round(time.Second).String()
		} else {
			pubAgo = "never"
		}

		fmt.Fprintf(w, `{"healthy":%t,"last_advertisement_ago":"%s","last_publish_ago":"%s"}`,
			healthy,
			advAge.Round(time.Second),
			pubAgo)
	})

	server := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", healthPort),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	logger.Info("Health server started", "port", healthPort)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("Health server failed", "error", err)
	}
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
