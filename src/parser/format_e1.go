package parser

import (
	"encoding/binary"
)

// Format E1 data structure:
// Offset | Field           | Type    | Unit    | Resolution
// 0      | Format ID       | uint8   | -       | 0xE1 (225)
// 1-2    | Temperature     | int16   | °C      | 0.005
// 3-4    | Humidity        | uint16  | %       | 0.0025
// 5-6    | Pressure        | uint16  | Pa      | 1 (+50000)
// 7-8    | PM1.0           | uint16  | µg/m³   | 0.1
// 9-10   | PM2.5           | uint16  | µg/m³   | 0.1
// 11-12  | PM4.0           | uint16  | µg/m³   | 0.1
// 13-14  | PM10.0          | uint16  | µg/m³   | 0.1
// 15-16  | CO2             | uint16  | ppm     | 1
// 17+    | VOC             | 9-bit   | index   | 1
// 18+    | NOx             | 9-bit   | index   | 1
// 19-21  | Luminosity      | uint24  | lux     | 0.01
// 25-27  | Sequence        | uint24  | -       | 1
// 28     | Flags           | uint8   | -       | -
// 34-39  | MAC             | bytes   | -       | -

const (
	formatE1MinLength = 28 // Minimum length to parse core values

	// Invalid values for Format E1
	formatE1InvalidTemp     = int16(-32768)   // 0x8000
	formatE1InvalidHumidity = uint16(65535)   // 0xFFFF
	formatE1InvalidPressure = uint16(65535)   // 0xFFFF
	formatE1InvalidPM       = uint16(65535)   // 0xFFFF
	formatE1InvalidCO2      = uint16(65535)   // 0xFFFF
	formatE1InvalidVOC      = uint16(511)     // 9-bit max (0x1FF)
	formatE1InvalidNOx      = uint16(511)     // 9-bit max (0x1FF)
	formatE1InvalidLux      = uint32(0xFFFFFF)// 24-bit max
)

// ParseFormatE1 parses Format E1 data
func ParseFormatE1(data []byte) (*Measurement, error) {
	// Validate minimum length
	if len(data) < formatE1MinLength {
		return nil, ErrInvalidDataLength
	}

	// Validate format ID
	if data[0] != FormatE1 {
		return nil, ErrInvalidFormat
	}

	m := &Measurement{
		Format: int(FormatE1), // 225
	}

	// Temperature (int16, 0.005 °C per unit)
	tempRaw := int16(binary.BigEndian.Uint16(data[1:3]))
	if tempRaw != formatE1InvalidTemp {
		tempC := float64(tempRaw) * 0.005
		if tempC < -40 || tempC > 85 {
			return nil, ErrTemperatureOutOfRange
		}
		temp := tempC
		m.Temperature = &temp
	}

	// Humidity (uint16, 0.0025 % per unit)
	humidityRaw := binary.BigEndian.Uint16(data[3:5])
	if humidityRaw != formatE1InvalidHumidity {
		humidity := float64(humidityRaw) * 0.0025
		if humidity > 100 {
			humidity = 100
		}
		m.Humidity = &humidity
	}

	// Pressure (uint16, 1 Pa per unit, offset +50000)
	pressureRaw := binary.BigEndian.Uint16(data[5:7])
	if pressureRaw != formatE1InvalidPressure {
		pressure := int(pressureRaw) + 50000
		m.Pressure = &pressure
	}

	// PM1.0 (uint16, 0.1 µg/m³ per unit)
	pm1Raw := binary.BigEndian.Uint16(data[7:9])
	if pm1Raw != formatE1InvalidPM {
		pm1 := float64(pm1Raw) * 0.1
		m.PM1_0 = &pm1
	}

	// PM2.5 (uint16, 0.1 µg/m³ per unit)
	pm25Raw := binary.BigEndian.Uint16(data[9:11])
	if pm25Raw != formatE1InvalidPM {
		pm25 := float64(pm25Raw) * 0.1
		m.PM2_5 = &pm25
	}

	// PM4.0 (uint16, 0.1 µg/m³ per unit)
	pm4Raw := binary.BigEndian.Uint16(data[11:13])
	if pm4Raw != formatE1InvalidPM {
		pm4 := float64(pm4Raw) * 0.1
		m.PM4_0 = &pm4
	}

	// PM10.0 (uint16, 0.1 µg/m³ per unit)
	pm10Raw := binary.BigEndian.Uint16(data[13:15])
	if pm10Raw != formatE1InvalidPM {
		pm10 := float64(pm10Raw) * 0.1
		m.PM10_0 = &pm10
	}

	// CO2 (uint16, 1 ppm per unit)
	co2Raw := binary.BigEndian.Uint16(data[15:17])
	if co2Raw != formatE1InvalidCO2 {
		co2 := int(co2Raw)
		m.CO2 = &co2
	}

	// VOC and NOx are packed as 9-bit values with flags
	// Bytes 17-19 contain: VOC (9 bits) + NOx (9 bits) + flags
	if len(data) >= 20 {
		// VOC index (9 bits starting at byte 17)
		vocRaw := uint16(data[17])<<1 | uint16(data[18]>>7)
		if vocRaw != formatE1InvalidVOC {
			voc := int(vocRaw)
			m.VOCIndex = &voc
		}

		// NOx index (9 bits starting at bit 7 of byte 18)
		noxRaw := uint16(data[18]&0x7F)<<2 | uint16(data[19]>>6)
		if noxRaw != formatE1InvalidNOx {
			nox := int(noxRaw)
			m.NOxIndex = &nox
		}
	}

	// Luminosity (uint24, 0.01 lux per unit) at bytes 19-21
	if len(data) >= 22 {
		luxRaw := uint32(data[19]&0x3F)<<16 | uint32(data[20])<<8 | uint32(data[21])
		if luxRaw != formatE1InvalidLux && luxRaw != (formatE1InvalidLux&0x3FFFFF) {
			lux := float64(luxRaw) * 0.01
			m.Luminosity = &lux
		}
	}

	// Sequence (uint24) at bytes 25-27
	if len(data) >= 28 {
		seqRaw := uint32(data[25])<<16 | uint32(data[26])<<8 | uint32(data[27])
		if seqRaw != 0xFFFFFF {
			seq := int(seqRaw)
			m.Sequence = &seq
		}
	}

	return m, nil
}
