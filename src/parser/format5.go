package parser

import (
	"encoding/binary"
)

// Format 5 (RAWv2) data structure:
// Offset | Field           | Type   | Unit    | Resolution
// 0      | Format ID       | uint8  | -       | 5
// 1-2    | Temperature     | int16  | °C      | 0.005
// 3-4    | Humidity        | uint16 | %       | 0.0025
// 5-6    | Pressure        | uint16 | Pa      | 1 (+50000)
// 7-8    | Acceleration X  | int16  | mG      | 1
// 9-10   | Acceleration Y  | int16  | mG      | 1
// 11-12  | Acceleration Z  | int16  | mG      | 1
// 13-14  | Power (11b) + TX (5b) | bits | mV/dBm | -
// 15     | Movement counter| uint8  | -       | 1
// 16-17  | Sequence        | uint16 | -       | 1
// 18-23  | MAC             | bytes  | -       | -

const (
	format5MinLength = 24

	// Invalid values for Format 5
	format5InvalidTemp     = int16(-32768)   // 0x8000
	format5InvalidHumidity = uint16(65535)   // 0xFFFF
	format5InvalidPressure = uint16(65535)   // 0xFFFF
	format5InvalidAccel    = int16(-32768)   // 0x8000
	format5InvalidPower    = uint16(0xFFFF)  // 11 bits voltage + 5 bits tx
	format5InvalidMovement = uint8(255)      // 0xFF
	format5InvalidSequence = uint16(65535)   // 0xFFFF
)

// ParseFormat5 parses Format 5 (RAWv2) data
func ParseFormat5(data []byte) (*Measurement, error) {
	// Validate minimum length
	if len(data) < format5MinLength {
		return nil, ErrInvalidDataLength
	}

	// Validate format ID
	if data[0] != Format5 {
		return nil, ErrInvalidFormat
	}

	m := &Measurement{
		Format: Format5,
	}

	// Temperature (int16, 0.005 °C per unit)
	tempRaw := int16(binary.BigEndian.Uint16(data[1:3]))
	if tempRaw != format5InvalidTemp {
		// Validate temperature range (-40°C to 85°C for Ruuvi sensors)
		tempC := float64(tempRaw) * 0.005
		if tempC < -40 || tempC > 85 {
			return nil, ErrTemperatureOutOfRange
		}
		temp := tempC
		m.Temperature = &temp
	}

	// Humidity (uint16, 0.0025 % per unit)
	humidityRaw := binary.BigEndian.Uint16(data[3:5])
	if humidityRaw != format5InvalidHumidity {
		humidity := float64(humidityRaw) * 0.0025
		if humidity > 100 {
			humidity = 100 // Cap at 100%
		}
		m.Humidity = &humidity
	}

	// Pressure (uint16, 1 Pa per unit, offset +50000)
	pressureRaw := binary.BigEndian.Uint16(data[5:7])
	if pressureRaw != format5InvalidPressure {
		pressure := int(pressureRaw) + 50000
		m.Pressure = &pressure
	}

	// Acceleration X (int16, mG)
	accelXRaw := int16(binary.BigEndian.Uint16(data[7:9]))
	if accelXRaw != format5InvalidAccel {
		accelX := int(accelXRaw)
		m.AccelerationX = &accelX
	}

	// Acceleration Y (int16, mG)
	accelYRaw := int16(binary.BigEndian.Uint16(data[9:11]))
	if accelYRaw != format5InvalidAccel {
		accelY := int(accelYRaw)
		m.AccelerationY = &accelY
	}

	// Acceleration Z (int16, mG)
	accelZRaw := int16(binary.BigEndian.Uint16(data[11:13]))
	if accelZRaw != format5InvalidAccel {
		accelZ := int(accelZRaw)
		m.AccelerationZ = &accelZ
	}

	// Power info (11 bits voltage + 5 bits TX power)
	powerRaw := binary.BigEndian.Uint16(data[13:15])
	if powerRaw != format5InvalidPower {
		// Voltage: bits 15-5 (11 bits), 1 mV per unit, offset +1600
		voltageRaw := powerRaw >> 5
		if voltageRaw != 0x7FF { // 0x7FF = invalid
			voltage := float64(voltageRaw+1600) / 1000.0
			m.BatteryVoltage = &voltage
		}

		// TX power: bits 4-0 (5 bits), 2 dBm per unit, offset -40
		txRaw := powerRaw & 0x1F
		if txRaw != 0x1F { // 0x1F = invalid
			txPower := int(txRaw)*2 - 40
			m.TxPower = &txPower
		}
	}

	// Movement counter (uint8)
	movementRaw := data[15]
	if movementRaw != format5InvalidMovement {
		movement := int(movementRaw)
		m.MovementCounter = &movement
	}

	// Measurement sequence (uint16)
	sequenceRaw := binary.BigEndian.Uint16(data[16:18])
	if sequenceRaw != format5InvalidSequence {
		sequence := int(sequenceRaw)
		m.Sequence = &sequence
	}

	return m, nil
}

// round rounds a float to the specified number of decimal places
func round(val float64, precision int) float64 {
	ratio := float64(1)
	for i := 0; i < precision; i++ {
		ratio *= 10
	}
	return float64(int(val*ratio+0.5)) / ratio
}
