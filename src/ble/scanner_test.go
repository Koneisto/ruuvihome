package ble

import (
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"

	"ruuvihome/config"
)

func TestMatchRulesNamedNarrowsToConfiguredTags(t *testing.T) {
	cfg := &config.Config{}
	cfg.Processing.FilterMode = "named"
	cfg.Devices = map[string]*config.DeviceConfig{"C8:8A:AB:3D:7B:E8": {}, "f0:31:80:ed:76:d2": {}}
	s := &Scanner{cfg: cfg, adapter: dbus.ObjectPath("/org/bluez/hci0")}
	rules := s.matchRules()
	if len(rules) != 4 {
		t.Fatalf("want 4 rules (2 per tag), got %d: %v", len(rules), rules)
	}
	all := strings.Join(rules, "\n")
	for _, want := range []string{
		"path='/org/bluez/hci0/dev_C8_8A_AB_3D_7B_E8'",
		"path='/org/bluez/hci0/dev_F0_31_80_ED_76_D2'", // lower-case config MAC normalised
		"arg0path='/org/bluez/hci0/dev_C8_8A_AB_3D_7B_E8'",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %s in\n%s", want, all)
		}
	}
	for _, r := range rules {
		if r == "type='signal',sender='org.bluez'" {
			t.Errorf("named mode must not subscribe to all BlueZ signals")
		}
	}
}

func TestMatchRulesAllKeepsBroadRule(t *testing.T) {
	cfg := &config.Config{}
	cfg.Processing.FilterMode = "all"
	s := &Scanner{cfg: cfg, adapter: dbus.ObjectPath("/org/bluez/hci0")}
	if r := s.matchRules(); len(r) != 1 || r[0] != "type='signal',sender='org.bluez'" {
		t.Fatalf("filter_mode=all must keep the broad rule, got %v", r)
	}
}

func TestRSSICacheUsedWhenUnchanged(t *testing.T) {
	var got []int
	s := &Scanner{cfg: &config.Config{}, logger: config.NewLogger("info", "simple"), lastRSSI: map[dbus.ObjectPath]int{},
		handler: func(_ string, rssi int, _ []byte) { got = append(got, rssi) }}
	path := dbus.ObjectPath("/org/bluez/hci0/dev_C8_8A_AB_3D_7B_E8")
	mfg := dbus.MakeVariant(map[uint16]dbus.Variant{RuuviManufacturerID: dbus.MakeVariant([]byte{5, 1, 2})})
	s.handleDeviceProperties(path, map[string]dbus.Variant{"ManufacturerData": mfg, "RSSI": dbus.MakeVariant(int16(-84))})
	s.handleDeviceProperties(path, map[string]dbus.Variant{"ManufacturerData": mfg}) // no RSSI, no bus → cache
	if len(got) != 2 || got[1] != -84 {
		t.Fatalf("want cached RSSI -84 for second advertisement, got %v", got)
	}
}
