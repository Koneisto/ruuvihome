package parser

import (
	"encoding/binary"
)

// Format 6 (RAWv2 with Air Quality) - Ruuvi Air
// 20 bytes payload based on observed data:
// Offset | Field           | Type   | Unit    | Resolution
// 0      | Format ID       | uint8  | -       | 6
// 1-2    | Temperature     | int16  | °C      | 0.005
// 3-4    | Humidity        | uint16 | %       | 0.0025
// 5-6    | Pressure        | uint16 | Pa      | 1 (+50000)
// 7-8    | PM2.5           | uint16 | µg/m³   | 0.1
// 9-10   | CO2             | uint16 | ppm     | 1
// 11     | VOC high bits   | uint8  | -       | packed
// 12     | NOx + VOC low   | uint8  | -       | packed
// 13     | Flags/reserved  | uint8  | -       | -
// 14     | Sequence        | uint8  | -       | 1
// 15-19  | MAC (partial)   | bytes  | -       | -

const (
	format6MinLength = 15

	format6InvalidTemp     = int16(-32768)
	format6InvalidHumidity = uint16(65535)
	format6InvalidPressure = uint16(65535)
	format6InvalidPM       = uint16(65535)
	format6InvalidCO2      = uint16(65535)
)

// ParseFormat6 parses Format 6 (RAWv2 with Air Quality) data
func ParseFormat6(data []byte) (*Measurement, error) {
	if len(data) < format6MinLength {
		return nil, ErrInvalidDataLength
	}

	if data[0] != Format6 {
		return nil, ErrInvalidFormat
	}

	m := &Measurement{
		Format: int(Format6),
	}

	// Temperature (int16, 0.005 °C per unit)
	tempRaw := int16(binary.BigEndian.Uint16(data[1:3]))
	if tempRaw != format6InvalidTemp {
		tempC := float64(tempRaw) * 0.005
		if tempC >= -40 && tempC <= 85 {
			temp := tempC
			m.Temperature = &temp
		}
	}

	// Humidity (uint16, 0.0025 % per unit)
	humidityRaw := binary.BigEndian.Uint16(data[3:5])
	if humidityRaw != format6InvalidHumidity {
		humidity := float64(humidityRaw) * 0.0025
		if humidity > 100 {
			humidity = 100
		}
		m.Humidity = &humidity
	}

	// Pressure (uint16, 1 Pa per unit, offset +50000)
	pressureRaw := binary.BigEndian.Uint16(data[5:7])
	if pressureRaw != format6InvalidPressure {
		pressure := int(pressureRaw) + 50000
		m.Pressure = &pressure
	}

	// PM2.5 (uint16, 0.1 µg/m³ per unit)
	if len(data) >= 9 {
		pm25Raw := binary.BigEndian.Uint16(data[7:9])
		if pm25Raw != format6InvalidPM && pm25Raw != 0 {
			pm25 := float64(pm25Raw) * 0.1
			m.PM2_5 = &pm25
		}
	}

	// CO2 (uint16, 1 ppm per unit)
	if len(data) >= 11 {
		co2Raw := binary.BigEndian.Uint16(data[9:11])
		if co2Raw != format6InvalidCO2 && co2Raw != 0 {
			co2 := int(co2Raw)
			m.CO2 = &co2
		}
	}

	// VOC and NOx - packed as 9-bit values in bytes 11-13
	// Similar to Format E1 bit packing for Sensirion SGP40/41 indices (1-500)
	if len(data) >= 14 {
		// VOC: 9 bits (byte 11 all 8 bits + byte 12 MSB)
		vocRaw := uint16(data[11])<<1 | uint16(data[12]>>7)
		if vocRaw != 511 && vocRaw > 0 { // 511 = 9-bit invalid
			voc := int(vocRaw)
			m.VOCIndex = &voc
		}

		// NOx: 9 bits (byte 12 bits 6-0 + byte 13 bits 7-6)
		noxRaw := uint16(data[12]&0x7F)<<2 | uint16(data[13]>>6)
		if noxRaw != 511 {
			nox := int(noxRaw)
			m.NOxIndex = &nox
		}
	}

	// Sequence number at byte 14
	if len(data) >= 15 {
		seqByte := data[14]
		if seqByte != 0xFF {
			seq := int(seqByte)
			m.Sequence = &seq
		}
	}

	return m, nil
}
