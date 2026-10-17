package clearsky

import (
	"math"
	"time"
)

const radians = math.Pi / 180

// ForecastHour reuses NOAA solar geometry across all forecast points.
type ForecastHour struct {
	ending  time.Time
	samples [4]forecastSolarSample
}
type forecastSolarSample struct {
	declination, equationMinutes, utcHour, extraterrestrial float64
}

// PrepareForecastHour describes the hour ending at ending, matching accumulated radiation.
func PrepareForecastHour(ending time.Time) ForecastHour {
	hour := ForecastHour{ending: ending.UTC()}
	for i := range hour.samples {
		at := hour.ending.Add(-time.Duration(450+i*900) * time.Second)
		utcHour := float64(at.Hour()) + float64(at.Minute())/60 + float64(at.Second())/3600
		day := float64(at.YearDay() - 1)
		gamma := 2 * math.Pi / 365 * (day + (utcHour-12)/24)
		angle := 2 * math.Pi / 365 * day
		hour.samples[i] = forecastSolarSample{
			declination:      .006918 - .399912*math.Cos(gamma) + .070257*math.Sin(gamma) - .006758*math.Cos(2*gamma) + .000907*math.Sin(2*gamma) - .002697*math.Cos(3*gamma) + .00148*math.Sin(3*gamma),
			equationMinutes:  229.18 * (.000075 + .001868*math.Cos(gamma) - .032077*math.Sin(gamma) - .014615*math.Cos(2*gamma) - .040849*math.Sin(2*gamma)),
			utcHour:          utcHour,
			extraterrestrial: 1366.1 * (1.00011 + .034221*math.Cos(angle) + .00128*math.Sin(angle) + .000719*math.Cos(2*angle) + .000077*math.Sin(2*angle)),
		}
	}
	return hour
}

// ForecastRadiation returns hour-mean Ineichen global horizontal and direct normal irradiance.
func (m *Model) ForecastRadiation(hour ForecastHour) (clearGlobalWm2, clearNormalWm2 float64) {
	latitude := m.latitude * radians
	sinLatitude, cosLatitude := math.Sincos(latitude)
	firstHeight, secondHeight := math.Exp(-m.altitude/8000), math.Exp(-m.altitude/1250)
	turbidity := m.turbidity(hour.ending.Year(), hour.ending.YearDay())
	for _, sample := range hour.samples {
		hourAngle := ((sample.utcHour*60+sample.equationMinutes+4*m.longitude)/4 - 180) * radians
		cosineZenith := sinLatitude*math.Sin(sample.declination) + cosLatitude*math.Cos(sample.declination)*math.Cos(hourAngle)
		if cosineZenith <= 0 {
			continue
		}
		elevation := math.Asin(cosineZenith) / radians
		airmass := m.pressure / 101325 / (cosineZenith + .50572*math.Pow(6.07995+elevation, -1.6364))
		global := (5.09e-5*m.altitude + .868) * sample.extraterrestrial * cosineZenith * math.Exp(-(3.92e-5*m.altitude+.0387)*airmass*(firstHeight+secondHeight*(turbidity-1)))
		beamOne := sample.extraterrestrial * (.664 + .163/firstHeight) * math.Exp(-.09*airmass*(turbidity-1))
		beamTwo := global * (1 - (.1-.2*math.Exp(-turbidity))/(.1+.882/firstHeight)) / cosineZenith
		clearGlobalWm2 += global / 4
		clearNormalWm2 += math.Min(beamOne, beamTwo) / 4
	}
	return
}

// EstimateForecastDNI distributes interval SSRD using clear-sky irradiance rather than nighttime hours.
func EstimateForecastDNI(intervalSolarWm2, intervalClearGlobalWm2, hourClearNormalWm2 float64) float64 {
	if intervalClearGlobalWm2 <= 0 || intervalSolarWm2 <= 0 {
		return 0
	}
	transmission := math.Max(0, math.Min(1, (intervalSolarWm2/intervalClearGlobalWm2-0.05)/0.95))
	return hourClearNormalWm2 * math.Pow(transmission, 2.1)
}
