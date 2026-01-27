package mqtt

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	json "github.com/goccy/go-json"
	mqtt "github.com/eclipse/paho.mqtt.golang"

	"ruuvihome/config"
	"ruuvihome/parser"
)

const (
	publishTimeout = 5 * time.Second
)

// Client wraps the MQTT client
type Client struct {
	client mqtt.Client
	cfg    *config.Config
	logger *config.Logger
	// Pre-allocated topic buffer
	topicBuf strings.Builder
}

// NewClient creates a new MQTT client
func NewClient(cfg *config.Config, logger *config.Logger) (*Client, error) {
	opts := mqtt.NewClientOptions()

	opts.AddBroker(cfg.MQTT.Broker)
	opts.SetClientID(cfg.MQTT.ClientID)

	if cfg.MQTT.Username != "" {
		opts.SetUsername(cfg.MQTT.Username)
	}
	if cfg.MQTT.Password != "" {
		opts.SetPassword(cfg.MQTT.Password)
	}

	// Configure TLS if using ssl:// or tls://
	if strings.HasPrefix(cfg.MQTT.Broker, "ssl://") || strings.HasPrefix(cfg.MQTT.Broker, "tls://") {
		tlsConfig := &tls.Config{
			MinVersion: tls.VersionTLS12,
		}

		if cfg.MQTT.CACert != "" {
			caCert, err := os.ReadFile(cfg.MQTT.CACert)
			if err != nil {
				return nil, fmt.Errorf("failed to read CA certificate: %w", err)
			}
			caCertPool := x509.NewCertPool()
			if !caCertPool.AppendCertsFromPEM(caCert) {
				return nil, fmt.Errorf("failed to parse CA certificate")
			}
			tlsConfig.RootCAs = caCertPool
		}

		opts.SetTLSConfig(tlsConfig)
	} else {
		// Warn about unencrypted connection
		logger.Warn("MQTT connection is not encrypted - consider using TLS (ssl:// or tls://)")
	}

	// Connection settings
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(5 * time.Second)
	opts.SetMaxReconnectInterval(1 * time.Minute)
	opts.SetKeepAlive(60 * time.Second)
	opts.SetCleanSession(true)

	// Last Will Testament
	lwt := fmt.Sprintf(`{"status":"offline","client_id":"%s"}`, cfg.MQTT.ClientID)
	opts.SetWill(fmt.Sprintf("%s/status", cfg.MQTT.TopicPrefix), lwt, 1, true)

	// Connection handlers
	opts.SetOnConnectHandler(func(c mqtt.Client) {
		logger.Info("MQTT connected")
		// Publish online status
		c.Publish(fmt.Sprintf("%s/status", cfg.MQTT.TopicPrefix),
			1, true, fmt.Sprintf(`{"status":"online","client_id":"%s"}`, cfg.MQTT.ClientID))
	})

	opts.SetConnectionLostHandler(func(c mqtt.Client, err error) {
		logger.Warn("MQTT connection lost", "error", err)
	})

	opts.SetReconnectingHandler(func(c mqtt.Client, opts *mqtt.ClientOptions) {
		logger.Info("MQTT reconnecting...")
	})

	client := mqtt.NewClient(opts)

	return &Client{
		client: client,
		cfg:    cfg,
		logger: logger,
	}, nil
}

// Connect connects to the MQTT broker
func (c *Client) Connect(ctx context.Context) error {
	token := c.client.Connect()

	// Use WaitTimeout instead of channel allocation
	if !token.WaitTimeout(30 * time.Second) {
		return fmt.Errorf("connection timeout")
	}

	if token.Error() != nil {
		return token.Error()
	}

	return nil
}

// Disconnect disconnects from the MQTT broker
func (c *Client) Disconnect() {
	// Publish offline status before disconnecting
	c.client.Publish(fmt.Sprintf("%s/status", c.cfg.MQTT.TopicPrefix),
		1, true, fmt.Sprintf(`{"status":"offline","client_id":"%s"}`, c.cfg.MQTT.ClientID))

	c.client.Disconnect(1000)
}

// Publish publishes a measurement to MQTT using fast JSON encoder
func (c *Client) Publish(ctx context.Context, m *parser.Measurement) error {
	// Build topic efficiently
	topic := c.cfg.MQTT.TopicPrefix + "/" + m.MAC

	// Serialize to JSON using goccy/go-json (3x faster than encoding/json)
	payload, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("failed to serialize measurement: %w", err)
	}

	// Publish with timeout (no channel allocation)
	token := c.client.Publish(topic, c.cfg.MQTT.QoS, c.cfg.MQTT.Retain, payload)

	// Check context cancellation
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Use WaitTimeout instead of channel
	if !token.WaitTimeout(publishTimeout) {
		return fmt.Errorf("publish timeout")
	}

	return token.Error()
}

// PublishRaw publishes raw bytes to a topic
func (c *Client) PublishRaw(ctx context.Context, topic string, payload []byte, qos byte, retain bool) error {
	token := c.client.Publish(topic, qos, retain, payload)

	// Check context cancellation
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Use WaitTimeout instead of channel
	if !token.WaitTimeout(publishTimeout) {
		return fmt.Errorf("publish timeout")
	}

	return token.Error()
}

// IsConnected returns true if the client is connected
func (c *Client) IsConnected() bool {
	return c.client.IsConnected()
}

// RateLimiter limits publishing frequency per device using sync.Map for better concurrency
type RateLimiter struct {
	interval time.Duration
	cfg      *config.Config
	lastSent sync.Map // map[string]time.Time - lock-free for reads
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(defaultInterval time.Duration, cfg *config.Config) *RateLimiter {
	return &RateLimiter{
		interval: defaultInterval,
		cfg:      cfg,
	}
}

// Allow returns true if the device is allowed to publish
func (r *RateLimiter) Allow(mac string) bool {
	now := time.Now()

	// Get device-specific interval or use default
	interval := r.interval
	if deviceCfg, ok := r.cfg.Devices[mac]; ok && deviceCfg.MinInterval > 0 {
		interval = deviceCfg.MinInterval
	}

	// Check if enough time has passed (lock-free read)
	if lastVal, ok := r.lastSent.Load(mac); ok {
		last := lastVal.(time.Time)
		if now.Sub(last) < interval {
			return false
		}
	}

	// Store new timestamp
	r.lastSent.Store(mac, now)
	return true
}
