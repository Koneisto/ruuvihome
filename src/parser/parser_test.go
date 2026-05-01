package parser

import (
	"math"
	"testing"
)

// helper: build a Format 5 frame from logical values.
func buildFormat5(temp_milliC int16, humidity025pct uint16, pressure_minus50000 uint16,
	accelX, accelY, accelZ int16, voltageOffset1600 uint16, txCode uint8,
	movement uint8, sequence uint16) []byte {
	buf := make([]byte, 24)
	buf[0] = Format5
	// temp big-endian int16
	tu := uint16(temp_milliC)
	buf[1] = byte(tu >> 8)
	buf[2] = byte(tu)
	// humidity
	buf[3] = byte(humidity025pct >> 8)
	buf[4] = byte(humidity025pct)
	// pressure
	buf[5] = byte(pressure_minus50000 >> 8)
	buf[6] = byte(pressure_minus50000)
	// acceleration
	for i, v := range []int16{accelX, accelY, accelZ} {
		u := uint16(v)
		buf[7+i*2] = byte(u >> 8)
		buf[8+i*2] = byte(u)
	}
	// power: 11 bits voltage, 5 bits tx
	power := (voltageOffset1600 << 5) | uint16(txCode&0x1F)
	buf[13] = byte(power >> 8)
	buf[14] = byte(power)
	// movement counter
	buf[15] = movement
	// sequence
	buf[16] = byte(sequence >> 8)
	buf[17] = byte(sequence)
	// MAC bytes (18-23) — not validated by parser
	return buf
}

func ptrFloat(v float64) *float64 { return &v }

func TestFormat5_TypicalIndoor(t *testing.T) {
	// 22.0 °C → 22.0/0.005 = 4400 (int16)
	// 50.0 % → 50.0/0.0025 = 20000
	// 100200 Pa → 100200-50000 = 50200
	// voltage 2.96 V → (2960-1600) = 1360
	frame := buildFormat5(4400, 20000, 50200, 12, -34, 1024, 1360, 22 /*tx*/, 5 /*movement*/, 4657)
	m, err := ParseFormat5(frame)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Temperature == nil || math.Abs(*m.Temperature-22.0) > 0.001 {
		t.Errorf("temperature: got %v, want 22.0", m.Temperature)
	}
	if m.Humidity == nil || math.Abs(*m.Humidity-50.0) > 0.001 {
		t.Errorf("humidity: got %v, want 50.0", m.Humidity)
	}
	if m.Pressure == nil || *m.Pressure != 100200 {
		t.Errorf("pressure: got %v, want 100200", m.Pressure)
	}
	if m.AccelerationX == nil || *m.AccelerationX != 12 {
		t.Errorf("accelX: got %v", m.AccelerationX)
	}
	if m.BatteryVoltage == nil || math.Abs(*m.BatteryVoltage-2.96) > 0.001 {
		t.Errorf("battery: got %v, want 2.96", m.BatteryVoltage)
	}
	if m.MovementCounter == nil || *m.MovementCounter != 5 {
		t.Errorf("movement: got %v", m.MovementCounter)
	}
	if m.Sequence == nil || *m.Sequence != 4657 {
		t.Errorf("sequence: got %v", m.Sequence)
	}
}

func TestFormat5_InvalidTemperature(t *testing.T) {
	// 0x8000 = invalid sentinel
	frame := buildFormat5(int16(format5InvalidTemp), 20000, 50200, 0, 0, 0, 1360, 22, 0, 0)
	m, err := ParseFormat5(frame)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Temperature != nil {
		t.Errorf("expected Temperature=nil, got %v", *m.Temperature)
	}
	// Humidity should still be parsed
	if m.Humidity == nil {
		t.Errorf("humidity should be set")
	}
}

func TestFormat5_TemperatureOutOfRange(t *testing.T) {
	// 100°C / 0.005 = 20000 — out of -40..85 range, must error
	frame := buildFormat5(20000, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	_, err := ParseFormat5(frame)
	if err == nil {
		t.Fatalf("expected ErrTemperatureOutOfRange, got nil")
	}
}

func TestFormat5_AllInvalid(t *testing.T) {
	frame := buildFormat5(int16(format5InvalidTemp), format5InvalidHumidity,
		format5InvalidPressure, format5InvalidAccel, format5InvalidAccel, format5InvalidAccel,
		0x7FF, 0x1F, format5InvalidMovement, format5InvalidSequence)
	m, err := ParseFormat5(frame)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// All optional fields should be nil
	if m.Temperature != nil || m.Humidity != nil || m.Pressure != nil ||
		m.AccelerationX != nil || m.AccelerationY != nil || m.AccelerationZ != nil ||
		m.BatteryVoltage != nil || m.TxPower != nil ||
		m.MovementCounter != nil || m.Sequence != nil {
		t.Errorf("expected all nullable fields nil, got: T=%v H=%v P=%v AX=%v AY=%v AZ=%v BV=%v TX=%v MC=%v SEQ=%v",
			m.Temperature, m.Humidity, m.Pressure,
			m.AccelerationX, m.AccelerationY, m.AccelerationZ,
			m.BatteryVoltage, m.TxPower,
			m.MovementCounter, m.Sequence)
	}
	// Format must still be set
	if m.Format != Format5 {
		t.Errorf("format: got %d, want %d", m.Format, Format5)
	}
}

func TestFormat5_TooShort(t *testing.T) {
	frame := []byte{0x05, 0x00, 0x00}
	_, err := ParseFormat5(frame)
	if err != ErrInvalidDataLength {
		t.Errorf("expected ErrInvalidDataLength, got %v", err)
	}
}

func TestFormat5_WrongFormatID(t *testing.T) {
	frame := make([]byte, 24)
	frame[0] = 0x99
	_, err := ParseFormat5(frame)
	if err != ErrInvalidFormat {
		t.Errorf("expected ErrInvalidFormat, got %v", err)
	}
}

func TestFormat5_HumidityCappedAt100(t *testing.T) {
	// 105 % raw → cap at 100
	// 105 / 0.0025 = 42000
	frame := buildFormat5(4400, 42000, 50200, 0, 0, 0, 1360, 22, 0, 0)
	m, err := ParseFormat5(frame)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Humidity == nil || *m.Humidity != 100.0 {
		t.Errorf("humidity should be capped at 100, got %v", m.Humidity)
	}
}

func TestParser_DispatchByFormat(t *testing.T) {
	p := New(false, nil)
	frame5 := buildFormat5(4400, 20000, 50200, 0, 0, 1024, 1360, 22, 0, 0)
	m, err := p.Parse(frame5)
	if err != nil {
		t.Fatalf("Format5 dispatch: %v", err)
	}
	if m.Format != Format5 {
		t.Errorf("expected Format=%d, got %d", Format5, m.Format)
	}

	frame_unknown := []byte{0xAB, 0x00, 0x00, 0x00}
	if _, err := p.Parse(frame_unknown); err != ErrInvalidFormat {
		t.Errorf("expected ErrInvalidFormat, got %v", err)
	}

	if _, err := p.Parse(nil); err != ErrInvalidDataLength {
		t.Errorf("expected ErrInvalidDataLength, got %v", err)
	}

	if _, err := p.Parse([]byte{}); err != ErrInvalidDataLength {
		t.Errorf("expected ErrInvalidDataLength on empty, got %v", err)
	}
}

func TestParser_ExtendedValuesEnabled(t *testing.T) {
	p := New(true, nil)
	// 22°C, 50% → known dewpoint ~11.1°C (Magnus, a=17.27, b=237.7)
	frame := buildFormat5(4400, 20000, 50200, 0, 0, 1024, 1360, 22, 0, 0)
	m, err := p.Parse(frame)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.DewPoint == nil {
		t.Fatalf("DewPoint should be calculated")
	}
	if math.Abs(*m.DewPoint-11.1) > 0.5 {
		t.Errorf("DewPoint: got %v, want ~11.1", *m.DewPoint)
	}
	if m.AbsoluteHumidity == nil {
		t.Errorf("AbsoluteHumidity should be calculated")
	}
	if m.AirDensity == nil {
		t.Errorf("AirDensity should be calculated when pressure present")
	}
}

func TestParser_ExtendedValuesDisabled(t *testing.T) {
	p := New(false, nil)
	frame := buildFormat5(4400, 20000, 50200, 0, 0, 1024, 1360, 22, 0, 0)
	m, err := p.Parse(frame)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.DewPoint != nil || m.AbsoluteHumidity != nil || m.AirDensity != nil {
		t.Errorf("extended values should be nil when disabled")
	}
}

func TestCalculateDewPoint_KnownValue(t *testing.T) {
	// 22°C, 50% RH → ~11.07°C (well-known reference)
	dp := calculateDewPointFast(22.0, 50.0)
	if math.Abs(dp-11.07) > 0.1 {
		t.Errorf("dewpoint(22,50): got %v, want ~11.07", dp)
	}
	// 0°C, 100% → dewpoint should equal temperature
	dp = calculateDewPointFast(0.0, 100.0)
	if math.Abs(dp) > 0.05 {
		t.Errorf("dewpoint(0,100): got %v, want ~0", dp)
	}
}

func TestCalculateAQI_PM25(t *testing.T) {
	pm25 := 12.0
	m := &Measurement{Format: Format6, PM2_5: &pm25}
	aqi := calculateAQI(m)
	if aqi == nil {
		t.Fatal("expected non-nil AQI")
	}
	if *aqi != 50 {
		t.Errorf("AQI(PM2.5=12): got %d, want 50", *aqi)
	}
}

func TestCalculateAQI_NoData(t *testing.T) {
	m := &Measurement{Format: Format6}
	if aqi := calculateAQI(m); aqi != nil {
		t.Errorf("expected nil AQI when no data, got %d", *aqi)
	}
}
