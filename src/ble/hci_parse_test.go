package ble

import (
	"bytes"
	"testing"
)

// Real-format LE Advertising Report: RuuviTag RAWv2 (format 5), address C8:8A:AB:3D:7B:E8, RSSI −69.
func ruuviEvent() []byte {
	ad := []byte{0x02, 0x01, 0x06, // flags
		0x1B, 0xFF, 0x99, 0x04, 0x05, 0x12, 0xFC, 0x53, 0x94, 0xC3, 0x7C, 0x00, 0x04, 0xFF, 0x9C, 0x04, 0x0C, 0xAC, 0x36, 0x42, 0x00, 0xCD, 0xCB, 0xB8, 0x33, 0x4C, 0x88, 0x4F}
	rep := append([]byte{0x03, 0x01, 0xE8, 0x7B, 0x3D, 0xAB, 0x8A, 0xC8, byte(len(ad))}, ad...)
	rep = append(rep, byte(0xBB)) // −69
	ev := []byte{hciEvLEMeta, byte(2 + len(rep)), hciSubevAdvReport, 1}
	return append(ev, rep...)
}

func TestParseAdvReport(t *testing.T) {
	r, err := parseAdvReports(ruuviEvent())
	if err != nil || len(r) != 1 {
		t.Fatalf("reports %v err %v", r, err)
	}
	if r[0].addr != "C8:8A:AB:3D:7B:E8" || r[0].rssi != -69 {
		t.Fatalf("addr %s rssi %d", r[0].addr, r[0].rssi)
	}
	p := manufacturerData(r[0].data, RuuviManufacturerID)
	if len(p) != 24 || p[0] != 0x05 || !bytes.Equal(p[:3], []byte{0x05, 0x12, 0xFC}) {
		t.Fatalf("payload % x", p)
	}
}

func TestParseTwoReportsAndTruncation(t *testing.T) {
	one := ruuviEvent()
	rep := one[4:]
	ev := append([]byte{hciEvLEMeta, byte(2 + 2*len(rep)), hciSubevAdvReport, 2}, append(append([]byte{}, rep...), rep...)...)
	if r, err := parseAdvReports(ev); err != nil || len(r) != 2 {
		t.Fatalf("two reports: %d %v", len(r), err)
	}
	if _, err := parseAdvReports(one[:len(one)-5]); err == nil {
		t.Fatal("truncated report not detected")
	}
	if r, _ := parseAdvReports([]byte{hciEvCmdComplete, 4, 1, 0x0C, 0x20, 0}); r != nil {
		t.Fatal("non-LE-meta event parsed as report")
	}
}

func TestManufacturerDataOtherCompanyAndMalformed(t *testing.T) {
	if manufacturerData([]byte{0x05, 0xFF, 0x4C, 0x00, 0x02, 0x15}, RuuviManufacturerID) != nil {
		t.Fatal("Apple data returned as Ruuvi")
	}
	if manufacturerData([]byte{0x1B, 0xFF, 0x99}, RuuviManufacturerID) != nil {
		t.Fatal("length beyond buffer accepted")
	}
	if manufacturerData([]byte{0x00, 0xFF}, RuuviManufacturerID) != nil {
		t.Fatal("zero-length AD accepted")
	}
}

func TestCmdResult(t *testing.T) {
	if st, _, ok := cmdResult([]byte{hciEvCmdComplete, 4, 1, 0x0C, 0x20, 0x00}, opLESetScanEnable); !ok || st != 0 {
		t.Fatal("command complete not matched")
	}
	if st, _, ok := cmdResult([]byte{hciEvCmdStatus, 4, 0x0C, 1, 0x0B, 0x20}, opLESetScanParam); !ok || st != 0x0C {
		t.Fatal("command status not matched")
	}
	if _, _, ok := cmdResult([]byte{hciEvCmdComplete, 4, 1, 0x03, 0x0C, 0x00}, opLESetScanEnable); ok {
		t.Fatal("other opcode matched")
	}
	// LE Read Local Supported Features: return parameters = 8 feature bytes (BT500: extended adv bit 12 + coded bit 11)
	_, ret, ok := cmdResult([]byte{hciEvCmdComplete, 12, 1, 0x03, 0x20, 0x00, 0xFF, 0x19, 0, 0, 0, 0, 0, 0}, opLEReadFeatures)
	if !ok || len(ret) != 8 || ret[1]&0x10 == 0 || ret[1]&0x08 == 0 {
		t.Fatalf("features % x", ret)
	}
}

func TestScanUnits(t *testing.T) {
	for _, c := range []struct {
		ms   float64
		want uint16
	}{{10, 16}, {0, 0x10}, {1, 4}, {100000, 0x4000}, {50, 80}} {
		if got := scanUnits(c.ms, 0x10); got != c.want {
			t.Errorf("scanUnits(%v) = %d, want %d", c.ms, got, c.want)
		}
	}
}

// Extended report (Ruuvi Air E1 as seen with btmon: primary LE 1M, secondary LE 2M): 24-byte header before the data.
func extEvent(status byte, data []byte) []byte {
	rep := []byte{0x01 | status<<5, 0x00, 0x01, 0x98, 0xEA, 0x3D, 0x4F, 0x7C, 0xF3, 0x01, 0x02, 0x03, 0x7F, 0xB0, 0, 0, 0, 0, 0, 0, 0, 0, 0, byte(len(data))}
	rep = append(rep, data...)
	return append([]byte{hciEvLEMeta, byte(2 + len(rep)), hciSubevExtAdv, 1}, rep...)
}

func e1AD() []byte {
	payload := append([]byte{0xE1, 0x0E, 0x9F, 0x4F, 0xB4, 0xCC, 0x68}, make([]byte, 33)...) // E1 is 40 bytes
	ad := []byte{0x02, 0x01, 0x06, byte(3 + len(payload)), 0xFF, 0x99, 0x04}
	return append(ad, payload...)
}

func TestParseExtendedReport(t *testing.T) {
	r, err := parseAdvReports(extEvent(0, e1AD()))
	if err != nil || len(r) != 1 {
		t.Fatalf("reports %v err %v", r, err)
	}
	if r[0].addr != "F3:7C:4F:3D:EA:98" || r[0].rssi != -80 || r[0].sid != 3 || r[0].status != 0 {
		t.Fatalf("addr %s rssi %d sid %d status %d", r[0].addr, r[0].rssi, r[0].sid, r[0].status)
	}
	p := manufacturerData(r[0].data, RuuviManufacturerID)
	if len(p) != 40 || p[0] != 0xE1 {
		t.Fatalf("E1 payload % x", p)
	}
	if _, err := parseAdvReports(extEvent(0, e1AD())[:20]); err == nil {
		t.Fatal("truncated extended report not detected")
	}
}

func TestFragmentReassembly(t *testing.T) {
	ad := e1AD()
	f := fragments{}
	a, _ := parseAdvReports(extEvent(1, ad[:20]))
	b, _ := parseAdvReports(extEvent(0, ad[20:]))
	if _, done := f.add(a[0]); done {
		t.Fatal("incomplete fragment delivered")
	}
	full, done := f.add(b[0])
	if !done || len(full) != len(ad) || manufacturerData(full, RuuviManufacturerID)[0] != 0xE1 {
		t.Fatalf("reassembly failed: %d bytes", len(full))
	}
	tr, _ := parseAdvReports(extEvent(2, ad[:10]))
	f.add(a[0])
	if _, done := f.add(tr[0]); done || len(f) != 0 {
		t.Fatal("truncated data must be dropped")
	}
}
