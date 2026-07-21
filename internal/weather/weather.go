// Package weather defines the weather facts and scoring rules used by every
// product surface. Providers translate their data into DailyMetrics; maps and
// place details both call Score.
package weather

import (
	"fmt"
	"math"
)

// DailyMetrics describes the local daytime weather for one grid cell and day.
// Temperatures are Celsius, rain is millimetres, solar energy is MJ/m², wind
// speeds are km/h, percentages are 0–100, and WetTimeFraction is 0–1.
// WetTimeFraction is the duration-weighted share of the daytime window with
// measurable precipitation; it is not a count of wet forecast steps. Climate
// sources that only provide monthly rain totals leave it unavailable.
type DailyMetrics struct {
	ComfortableTempC        float64
	MinTempC                float64
	MaxTempC                float64
	RainMM                  float64
	WetTimeFraction         OptionalFloat
	SolarEnergyMJm2         OptionalFloat
	CloudCoverPercent       OptionalFloat
	WindSpeedKPH            float64
	GustKPH                 OptionalFloat
	DewPointC               OptionalFloat
	RelativeHumidityPercent OptionalFloat
	NativeResolutionKM      float64
	Valid                   bool
}

// OptionalFloat represents evidence that a provider may not supply. Missing
// values are explicit and must be zero, avoiding sentinel values and NaNs.
type OptionalFloat struct {
	Value     float64
	Available bool
}

// Validate rejects incomplete or physically implausible provider output.
func (m DailyMetrics) Validate() error {
	if !m.Valid {
		return fmt.Errorf("weather data is marked invalid")
	}
	values := []float64{
		m.ComfortableTempC, m.MinTempC, m.MaxTempC, m.RainMM,
		m.WetTimeFraction.Value, m.SolarEnergyMJm2.Value, m.CloudCoverPercent.Value,
		m.WindSpeedKPH, m.GustKPH.Value, m.DewPointC.Value,
		m.RelativeHumidityPercent.Value, m.NativeResolutionKM,
	}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("weather data contains a non-finite value: comfort=%.9g min=%.9g max=%.9g rain=%.9g wet-time=%.9g solar=%.9g cloud=%.9g wind=%.9g gust=%.9g dew-point=%.9g humidity=%.9g resolution=%.9g", m.ComfortableTempC, m.MinTempC, m.MaxTempC, m.RainMM, m.WetTimeFraction.Value, m.SolarEnergyMJm2.Value, m.CloudCoverPercent.Value, m.WindSpeedKPH, m.GustKPH.Value, m.DewPointC.Value, m.RelativeHumidityPercent.Value, m.NativeResolutionKM)
		}
	}
	if m.MinTempC > m.MaxTempC {
		return fmt.Errorf("minimum temperature exceeds maximum temperature: min=%.9g°C max=%.9g°C", m.MinTempC, m.MaxTempC)
	}
	if m.MinTempC < -100 || m.MaxTempC > 70 || m.ComfortableTempC < -100 || m.ComfortableTempC > 70 ||
		(m.DewPointC.Available && (m.DewPointC.Value < -100 || m.DewPointC.Value > 70)) {
		return fmt.Errorf("temperature is outside the supported Celsius range: comfort=%.9g°C min=%.9g°C max=%.9g°C dew-point=%.9g°C", m.ComfortableTempC, m.MinTempC, m.MaxTempC, m.DewPointC.Value)
	}
	if m.ComfortableTempC < m.MinTempC-5 || m.ComfortableTempC > m.MaxTempC+5 {
		return fmt.Errorf("comfortable temperature is inconsistent with the daily range: comfort=%.9g°C min=%.9g°C max=%.9g°C", m.ComfortableTempC, m.MinTempC, m.MaxTempC)
	}
	if m.RainMM < 0 || m.SolarEnergyMJm2.Value < 0 || m.WindSpeedKPH < 0 || m.GustKPH.Value < 0 {
		return fmt.Errorf("rain, solar energy and wind cannot be negative: rain=%.9g mm solar=%.9g MJ/m² wind=%.9g km/h gust=%.9g km/h", m.RainMM, m.SolarEnergyMJm2.Value, m.WindSpeedKPH, m.GustKPH.Value)
	}
	if m.WetTimeFraction.Value < 0 || m.WetTimeFraction.Value > 1 {
		return fmt.Errorf("wet-time fraction must be between 0 and 1: wet-time=%.9g", m.WetTimeFraction.Value)
	}
	if m.CloudCoverPercent.Value < 0 || m.CloudCoverPercent.Value > 100 || m.RelativeHumidityPercent.Value < 0 || m.RelativeHumidityPercent.Value > 100 {
		return fmt.Errorf("cloud cover and humidity must be between 0 and 100: cloud=%.9g%% humidity=%.9g%%", m.CloudCoverPercent.Value, m.RelativeHumidityPercent.Value)
	}
	if err := validateOptional("wet time", m.WetTimeFraction); err != nil {
		return err
	}
	if err := validateOptional("solar energy", m.SolarEnergyMJm2); err != nil {
		return err
	}
	if err := validateOptional("cloud cover", m.CloudCoverPercent); err != nil {
		return err
	}
	if err := validateOptional("gust", m.GustKPH); err != nil {
		return err
	}
	if err := validateOptional("dew point", m.DewPointC); err != nil {
		return err
	}
	if err := validateOptional("humidity", m.RelativeHumidityPercent); err != nil {
		return err
	}
	if !m.SolarEnergyMJm2.Available && !m.CloudCoverPercent.Available {
		return fmt.Errorf("either solar energy or cloud cover is required")
	}
	if !m.DewPointC.Available && !m.RelativeHumidityPercent.Available {
		return fmt.Errorf("either dew point or humidity is required")
	}
	if m.DewPointC.Available && m.DewPointC.Value > m.MaxTempC+2 {
		return fmt.Errorf("dew point materially exceeds maximum temperature: dew-point=%.9g°C max=%.9g°C", m.DewPointC.Value, m.MaxTempC)
	}
	if m.GustKPH.Available && m.GustKPH.Value+3 < m.WindSpeedKPH {
		return fmt.Errorf("gust speed is materially below sustained wind: gust=%.9g km/h wind=%.9g km/h", m.GustKPH.Value, m.WindSpeedKPH)
	}
	if m.NativeResolutionKM <= 0 {
		return fmt.Errorf("native resolution must be positive: resolution=%.9g km", m.NativeResolutionKM)
	}
	if m.RainMM > 1000 || m.SolarEnergyMJm2.Value > 60 || m.WindSpeedKPH > 500 || m.GustKPH.Value > 500 || m.NativeResolutionKM > 1000 {
		return fmt.Errorf("weather data is outside supported physical limits: rain=%.9g mm solar=%.9g MJ/m² wind=%.9g km/h gust=%.9g km/h resolution=%.9g km", m.RainMM, m.SolarEnergyMJm2.Value, m.WindSpeedKPH, m.GustKPH.Value, m.NativeResolutionKM)
	}
	return nil
}

func validateOptional(name string, value OptionalFloat) error {
	if !value.Available && value.Value != 0 {
		return fmt.Errorf("missing %s must have a zero value", name)
	}
	return nil
}
