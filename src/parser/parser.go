package parser

import (
	"errors"
	"math"

	"ruuvihome/config"
)

// Format identifiers
const (
	Format5  = 0x05 // RAWv2 (RuuviTag)
	Format6  = 0x06 // RAWv2 with Air Quality (Ruuvi Air)
	FormatE1 = 0xE1 // Alternative format
)

// Errors
var (
	ErrInvalidDataLength     = errors.New("invalid data length")
	ErrInvalidFormat         = errors.New("unsupported format")
	ErrInvalidTemperature    = errors.New("invalid temperature value")
	ErrTemperatureOutOfRange = errors.New("temperature out of valid range")
	ErrInvalidHumidity       = errors.New("invalid humidity value")
	ErrInvalidPressure       = errors.New("invalid pressure value")
)

// Measurement represents a parsed Ruuvi measurement
type Measurement struct {
	// Metadata
	MAC       string `json:"mac"`
	Name      string `json:"name,omitempty"`
	Timestamp int64  `json:"timestamp"`
	Format    int    `json:"format"`
	RSSI      int    `json:"rssi"`

	// Common fields (Format 5 & E1)
	Temperature *float64 `json:"temperature,omitempty"`
	Humidity    *float64 `json:"humidity,omitempty"`
	Pressure    *int     `json:"pressure,omitempty"`

	// Format 5 specific
	AccelerationX   *int     `json:"acceleration_x,omitempty"`
	AccelerationY   *int     `json:"acceleration_y,omitempty"`
	AccelerationZ   *int     `json:"acceleration_z,omitempty"`
	BatteryVoltage  *float64 `json:"battery_voltage,omitempty"`
	TxPower         *int     `json:"tx_power,omitempty"`
	MovementCounter *int     `json:"movement_counter,omitempty"`
	Sequence        *int     `json:"sequence,omitempty"`

	// Format E1 specific
	PM1_0      *float64 `json:"pm1_0,omitempty"`
	PM2_5      *float64 `json:"pm2_5,omitempty"`
	PM4_0      *float64 `json:"pm4_0,omitempty"`
	PM10_0     *float64 `json:"pm10_0,omitempty"`
	CO2        *int     `json:"co2,omitempty"`
	VOCIndex   *int     `json:"voc_index,omitempty"`
	NOxIndex   *int     `json:"nox_index,omitempty"`
	Luminosity *float64 `json:"luminosity,omitempty"`

	// Extended/calculated values
	DewPoint         *float64 `json:"dew_point,omitempty"`
	AbsoluteHumidity *float64 `json:"absolute_humidity,omitempty"`
	AirDensity       *float64 `json:"air_density,omitempty"`
	AirQualityIndex  *int     `json:"air_quality_index,omitempty"` // For Ruuvi AIR
}

// Parser handles parsing of Ruuvi data formats
type Parser struct {
	extendedValues bool
	logger         *config.Logger
}

// New creates a new parser
func New(extendedValues bool, logger *config.Logger) *Parser {
	return &Parser{
		extendedValues: extendedValues,
		logger:         logger,
	}
}

// Parse parses manufacturer data and returns a Measurement
func (p *Parser) Parse(data []byte) (*Measurement, error) {
	if len(data) < 1 {
		return nil, ErrInvalidDataLength
	}

	format := data[0]

	var m *Measurement
	var err error

	switch format {
	case Format5:
		m, err = ParseFormat5(data)
	case Format6:
		m, err = ParseFormat6(data)
	case FormatE1:
		// TODO: Format E1 uses Bluetooth 5 extended advertising, which is not
		// yet supported by the go-ble HCI library. The D-Bus backend receives
		// E1 data correctly since BlueZ handles extended advertising natively.
		// Parsing is implemented but reception depends on the active backend.
		m, err = ParseFormatE1(data)
	default:
		return nil, ErrInvalidFormat
	}

	if err != nil {
		return nil, err
	}

	// Calculate extended values if enabled
	if p.extendedValues {
		p.calculateExtendedValues(m)
	}

	return m, nil
}

// calculateExtendedValues calculates derived values with optimized math
func (p *Parser) calculateExtendedValues(m *Measurement) {
	// Need temperature and humidity for all calculations
	if m.Temperature == nil || m.Humidity == nil {
		// Still calculate AQI for Ruuvi AIR if we have the data
		if m.Format == Format6 || m.Format == FormatE1 {
			if aqi := calculateAQI(m); aqi != nil {
				m.AirQualityIndex = aqi
			}
		}
		return
	}

	temp := *m.Temperature
	humidity := *m.Humidity

	// Calculate saturation vapor pressure ONCE (expensive math.Exp call)
	// es = 610.78 * e^((17.27 * T) / (T + 237.3))
	es := 610.78 * math.Exp((17.27*temp)/(temp+237.3))

	// Actual vapor pressure
	e := es * humidity / 100.0

	// Dew point using Magnus formula
	dewPoint := calculateDewPointFast(temp, humidity)
	m.DewPoint = &dewPoint

	// Absolute humidity using pre-calculated vapor pressure
	absHumidity := calculateAbsoluteHumidityFast(temp, e)
	m.AbsoluteHumidity = &absHumidity

	// Air density if we have pressure
	if m.Pressure != nil {
		airDensity := calculateAirDensityFast(temp, e, *m.Pressure)
		m.AirDensity = &airDensity
	}

	// Calculate Air Quality Index (Format 6 and E1)
	if m.Format == Format6 || m.Format == FormatE1 {
		if aqi := calculateAQI(m); aqi != nil {
			m.AirQualityIndex = aqi
		}
	}
}

// calculateDewPointFast calculates dew point using Magnus formula
func calculateDewPointFast(temp, humidity float64) float64 {
	const a = 17.27
	const b = 237.7

	alpha := (a*temp)/(b+temp) + math.Log(humidity/100.0)
	dewPoint := (b * alpha) / (a - alpha)

	return dewPoint
}

// calculateAbsoluteHumidityFast calculates absolute humidity using pre-calculated vapor pressure
func calculateAbsoluteHumidityFast(temp, vaporPressure float64) float64 {
	const mw = 18.016 // Molar mass of water g/mol
	const r = 8.314   // Universal gas constant J/(mol*K)

	// Absolute humidity (g/m3)
	absHumidity := (vaporPressure * mw) / (r * (temp + 273.15))

	return absHumidity
}

// calculateAirDensityFast calculates air density using pre-calculated vapor pressure
func calculateAirDensityFast(temp, vaporPressure float64, pressure int) float64 {
	const rd = 287.058 // Specific gas constant for dry air J/(kg*K)
	const rv = 461.495 // Specific gas constant for water vapor J/(kg*K)

	// Temperature in Kelvin
	tk := temp + 273.15

	// Dry air pressure
	pd := float64(pressure) - vaporPressure

	// Air density using ideal gas law for humid air
	density := (pd / (rd * tk)) + (vaporPressure / (rv * tk))

	return density
}

// calculateAQI calculates an Air Quality Index for Ruuvi AIR
// Based on PM2.5, VOC, and CO2 levels
func calculateAQI(m *Measurement) *int {
	var maxScore int
	hasScore := false

	// PM2.5 based score (0-500 scale)
	if m.PM2_5 != nil {
		pm25 := *m.PM2_5
		var score int
		switch {
		case pm25 <= 12:
			score = int(pm25 * 50 / 12)
		case pm25 <= 35.4:
			score = 50 + int((pm25-12)*50/23.4)
		case pm25 <= 55.4:
			score = 100 + int((pm25-35.4)*50/20)
		case pm25 <= 150.4:
			score = 150 + int((pm25-55.4)*50/95)
		case pm25 <= 250.4:
			score = 200 + int((pm25-150.4)*100/100)
		default:
			score = 300 + int((pm25-250.4)*200/250)
		}
		if score > 500 {
			score = 500
		}
		if score > maxScore {
			maxScore = score
		}
		hasScore = true
	}

	// VOC index score
	if m.VOCIndex != nil {
		voc := *m.VOCIndex
		var score int
		switch {
		case voc <= 100:
			score = voc / 2
		case voc <= 200:
			score = 50 + (voc-100)/2
		case voc <= 300:
			score = 100 + (voc-200)/2
		case voc <= 400:
			score = 150 + (voc-300)/2
		default:
			score = 200 + (voc-400)/2
		}
		if score > 500 {
			score = 500
		}
		if score > maxScore {
			maxScore = score
		}
		hasScore = true
	}

	// CO2 based score
	if m.CO2 != nil {
		co2 := *m.CO2
		var score int
		switch {
		case co2 <= 600:
			score = co2 * 50 / 600
		case co2 <= 1000:
			score = 50 + (co2-600)*50/400
		case co2 <= 1500:
			score = 100 + (co2-1000)*50/500
		case co2 <= 2000:
			score = 150 + (co2-1500)*50/500
		default:
			score = 200 + (co2-2000)*100/1000
		}
		if score > 500 {
			score = 500
		}
		if score > maxScore {
			maxScore = score
		}
		hasScore = true
	}

	if !hasScore {
		return nil
	}

	return &maxScore
}
