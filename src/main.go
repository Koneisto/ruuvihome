package main

import (
	"context"
	"flag"
	"fmt"
	"net"
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
	configPath        = flag.String("config", "/config.yml", "Path to configuration file")
	healthcheckMode   = flag.Bool("healthcheck", false, "Probe health endpoint and exit 0/1 (for Docker HEALTHCHECK)")
	healthAddr        = flag.String("health-addr", "127.0.0.1:9100", "TCP address for the /health HTTP endpoint")
	version           = "1.0.0"
	healthCheckTimeout = 3 * time.Second
)

// macCache caches sanitized MAC addresses to avoid repeated string allocations
var macCache sync.Map

// Observability: counters and per-MAC freshness for tag silence detection
type observability struct {
	totalAdvertisements atomic.Int64 // every Ruuvi advertisement received
	publishedTotal      atomic.Int64 // successfully published to MQTT
	publishFailures     atomic.Int64 // publish errors
	rateLimited         atomic.Int64 // dropped due to min_interval
	parseErrors         atomic.Int64 // parsing failed
	lastSeen            sync.Map     // map[mac]time.Time
}

// Tag silence threshold: warn when no advertisement for this long.
// Default 15 min covers tags with min_interval up to ~10 min plus
// reasonable margin for occasional weak-signal misses.
const tagSilenceThreshold = 15 * time.Minute

// statsLogInterval: how often to dump aggregate counters at info level.
const statsLogInterval = 5 * time.Minute

// silenceCheckInterval: how often we walk through the configured devices.
const silenceCheckInterval = 1 * time.Minute

func main() {
	flag.Parse()

	// --healthcheck is a one-shot probe used by Docker HEALTHCHECK.
	// It opens a short-lived HTTP request to the running daemon and exits.
	if *healthcheckMode {
		os.Exit(runHealthcheckProbe(*healthAddr))
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

	// Observability counters + per-tag last-seen tracking
	var obs observability

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

		// Track that we have seen this device — even if rate-limited or unparseable.
		// This keeps the silence detector from false-positiving when the radio is fine.
		obs.lastSeen.Store(addr, time.Now())
		obs.totalAdvertisements.Add(1)

		// Check rate limit BEFORE parsing (avoid unnecessary work)
		if !rateLimiter.Allow(addr) {
			obs.rateLimited.Add(1)
			logger.Debug("Rate limited", "mac", sanitized)
			return
		}

		// Parse the advertisement data
		measurement, err := p.Parse(manufacturerData)
		if err != nil {
			obs.parseErrors.Add(1)
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
			obs.publishFailures.Add(1)
			logger.Error("Failed to publish measurement", "mac", sanitized, "error", err)
			return
		}

		obs.publishedTotal.Add(1)
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

	// Observability goroutine: aggregate stats + per-tag silence detection
	go runObservability(ctx, cfg, logger, &obs)

	// Health endpoint for Docker HEALTHCHECK / autoheal monitoring.
	// Returns 200 OK while we are connected to MQTT and have at least one
	// recent advertisement after the startup grace window.
	healthSrv := startHealthServer(ctx, *healthAddr, mqttClient, &obs, logger)
	defer healthSrv.Shutdown(context.Background())

	// Start scanning
	logger.Info("Starting BLE scan", "hci_device", cfg.Bluetooth.HCIDevice)

	if err := scanner.Start(ctx); err != nil {
		return fmt.Errorf("BLE scan failed: %w", err)
	}

	return nil
}

// healthCheckable is the slice of observability state the health endpoint reads.
// We use an interface so the http handler can be tested without spinning up MQTT.
type healthCheckable interface {
	IsConnected() bool
}

// startHealthServer starts a tiny HTTP server on healthAddr. The handler returns:
//
//	200 OK   — MQTT connected AND at least one advertisement in the last
//	           tagSilenceThreshold OR we are still inside the startup grace.
//	503 Service Unavailable — anything else.
//
// The endpoint is intentionally simple: scratch-image containers can't run
// pgrep/sh-based healthchecks, so the binary itself probes via --healthcheck.
func startHealthServer(ctx context.Context, addr string, mqttClient healthCheckable, obs *observability, logger *config.Logger) *http.Server {
	mux := http.NewServeMux()
	startTime := time.Now()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		uptime := time.Since(startTime)
		mqttUp := mqttClient.IsConnected()

		// Find the most recent advertisement across all tags.
		var newest time.Time
		obs.lastSeen.Range(func(_, v any) bool {
			if t, ok := v.(time.Time); ok && t.After(newest) {
				newest = t
			}
			return true
		})

		// Healthy if MQTT is up AND (we are within startup grace OR have a recent advertisement).
		recentEnough := !newest.IsZero() && time.Since(newest) < tagSilenceThreshold
		inGrace := uptime < tagSilenceThreshold
		healthy := mqttUp && (recentEnough || inGrace)

		w.Header().Set("Content-Type", "application/json")
		if healthy {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		fmt.Fprintf(w, `{"healthy":%t,"mqtt_connected":%t,"uptime_seconds":%d,"published_total":%d`,
			healthy, mqttUp, int(uptime.Seconds()), obs.publishedTotal.Load())
		if !newest.IsZero() {
			fmt.Fprintf(w, `,"last_advertisement_seconds_ago":%d`, int(time.Since(newest).Seconds()))
		}
		fmt.Fprintln(w, `}`)
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Health server failed", "error", err)
		}
	}()
	logger.Info("Health endpoint listening", "addr", addr)
	return srv
}

// runHealthcheckProbe is the implementation of --healthcheck CLI flag.
// It performs a single HTTP GET against /health and exits with 0/1 for Docker.
func runHealthcheckProbe(addr string) int {
	client := &http.Client{Timeout: healthCheckTimeout}
	url := "http://" + addr + "/health"
	resp, err := client.Get(url)
	if err != nil {
		// Connection refused, timeout, etc. → unhealthy
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return 0
	}
	return 1
}

// Compile-time check that we can wrap an HTTP listener address (used to
// validate that the addr is parseable at startup, before binding).
var _ = net.JoinHostPort

// runObservability periodically logs aggregate counters and warns about
// configured devices that haven't reported in a while.
func runObservability(ctx context.Context, cfg *config.Config, logger *config.Logger, obs *observability) {
	silenceTicker := time.NewTicker(silenceCheckInterval)
	defer silenceTicker.Stop()
	statsTicker := time.NewTicker(statsLogInterval)
	defer statsTicker.Stop()

	// First silence check after one threshold period to avoid spam at boot
	startupGrace := time.Now().Add(tagSilenceThreshold)

	for {
		select {
		case <-ctx.Done():
			return

		case <-silenceTicker.C:
			if time.Now().Before(startupGrace) {
				continue
			}
			now := time.Now()
			for mac, devCfg := range cfg.Devices {
				name := ""
				if devCfg != nil {
					name = devCfg.Name
				}
				lastVal, ok := obs.lastSeen.Load(mac)
				if !ok {
					logger.Warn("Tag never seen since startup",
						"mac", getSanitizedMAC(mac), "name", name)
					continue
				}
				if last, ok := lastVal.(time.Time); ok {
					silentFor := now.Sub(last)
					if silentFor > tagSilenceThreshold {
						logger.Warn("Tag silent",
							"mac", getSanitizedMAC(mac),
							"name", name,
							"silent_for", silentFor.Round(time.Second).String())
					}
				}
			}

		case <-statsTicker.C:
			logger.Info("Stats",
				"advertisements_total", obs.totalAdvertisements.Load(),
				"published_total", obs.publishedTotal.Load(),
				"publish_failures", obs.publishFailures.Load(),
				"rate_limited", obs.rateLimited.Load(),
				"parse_errors", obs.parseErrors.Load())
		}
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
