package config

import "testing"

func TestBluetoothModeAndBackendAlias(t *testing.T) {
	for _, c := range []struct {
		mode, backend, want string
		ok                  bool
	}{
		{"", "", "dbus", true}, {"hci", "", "hci", true}, {"dbus", "hci", "dbus", true},
		{"", "hci", "hci", true}, {"", "auto", "dbus", true}, {"", "dbus", "dbus", true},
		{"", "bogus", "", false}, {"bogus", "", "", false},
	} {
		cfg := &Config{MQTT: MQTTConfig{Broker: "tcp://127.0.0.1:1883", TopicPrefix: "ruuvi"}, Processing: ProcessingConfig{FilterMode: "all"}, Logging: LoggingConfig{Level: "info", Format: "simple"}}
		cfg.Bluetooth.Mode, cfg.Bluetooth.Backend = c.mode, c.backend
		err := cfg.Validate()
		if (err == nil) != c.ok || (c.ok && cfg.Bluetooth.Mode != c.want) {
			t.Errorf("mode %q backend %q: got %q err %v", c.mode, c.backend, cfg.Bluetooth.Mode, err)
		}
	}
}
