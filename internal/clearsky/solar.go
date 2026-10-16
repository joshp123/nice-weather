// NREL SPA solar geometry adapted from pvlib 0.16.1 (BSD-3-Clause).
package clearsky

import (
	"math"
	"time"
)

const radians = math.Pi / 180

func cosineSeries(coefficients [][3]float64, value float64) float64 {
	sum := 0.0
	for _, row := range coefficients {
		sum += row[0] * math.Cos(row[1]+row[2]*value)
	}
	return sum
}
func prepareSolarSample(t time.Time) solarSample {
	julianDay := float64(t.UnixNano())/1e9/86400 + 2440587.5
	century := (julianDay - 2451545) / 36525
	ephemerisCentury := (julianDay + 67.0/86400 - 2451545) / 36525
	millennium := ephemerisCentury / 10
	longitude := (cosineSeries(L0[:], millennium) + millennium*(cosineSeries(L1[:], millennium)+millennium*(cosineSeries(L2[:], millennium)+millennium*(cosineSeries(L3[:], millennium)+millennium*(cosineSeries(L4[:], millennium)+millennium*cosineSeries(L5[:], millennium)))))) / 1e8 / radians
	latitude := (cosineSeries(B0[:], millennium) + millennium*cosineSeries(B1[:], millennium)) / 1e8 / radians
	radius := (cosineSeries(R0[:], millennium) + millennium*(cosineSeries(R1[:], millennium)+millennium*(cosineSeries(R2[:], millennium)+millennium*(cosineSeries(R3[:], millennium)+millennium*cosineSeries(R4[:], millennium))))) / 1e8
	c := ephemerisCentury
	c2 := c * c
	c3 := c2 * c
	arguments := [5]float64{297.85036 + 445267.111480*c - 0.0019142*c2 + c3/189474, 357.52772 + 35999.050340*c - 0.0001603*c2 - c3/300000, 134.96298 + 477198.867398*c + 0.0086972*c2 + c3/56250, 93.27191 + 483202.017538*c - 0.0036825*c2 + c3/327270, 125.04452 - 1934.136261*c + 0.0020708*c2 + c3/450000}
	longitudeNutation, obliquityNutation := 0.0, 0.0
	for row, coefficients := range NUTATION_ABCD_ARRAY {
		argument := 0.0
		for index, value := range NUTATION_YTERM_ARRAY[row] {
			argument += value * arguments[index]
		}
		argument *= radians
		longitudeNutation += (coefficients[0] + coefficients[1]*c) * math.Sin(argument)
		obliquityNutation += (coefficients[2] + coefficients[3]*c) * math.Cos(argument)
	}
	longitudeNutation /= 36000000
	obliquityNutation /= 36000000
	u := millennium / 10
	meanObliquity := 84381.448 + u*(-4680.93+u*(-1.55+u*(1999.25+u*(-51.38+u*(-249.67+u*(-39.05+u*(7.12+u*(27.87+u*(5.79+u*2.45)))))))))
	obliquity := (meanObliquity/3600 + obliquityNutation) * radians
	apparentLongitude := (positiveDegrees(longitude+180) + longitudeNutation - 20.4898/(3600*radius)) * radians
	beta := -latitude * radians
	rightAscension := positiveDegrees(math.Atan2(math.Sin(apparentLongitude)*math.Cos(obliquity)-math.Tan(beta)*math.Sin(obliquity), math.Cos(apparentLongitude)) / radians)
	declination := math.Asin(math.Sin(beta)*math.Cos(obliquity) + math.Cos(beta)*math.Sin(obliquity)*math.Sin(apparentLongitude))
	sidereal := positiveDegrees(280.46061837+360.98564736629*(julianDay-2451545)+0.000387933*century*century-century*century*century/38710000) + longitudeNutation*math.Cos(obliquity)
	return solarSample{rightAscension: rightAscension, declination: declination, sidereal: sidereal, parallax: 8.794 / (3600 * radius) * radians, dayOfYear: t.YearDay(), year: t.Year()}
}
func (m *Model) apparentElevation(sample solarSample) float64 {
	latitude := m.latitude * radians
	hourAngle := positiveDegrees(sample.sidereal+m.longitude-sample.rightAscension) * radians
	u := math.Atan(0.99664719 * math.Tan(latitude))
	x := math.Cos(u) + m.altitude/6378140*math.Cos(latitude)
	y := 0.99664719*math.Sin(u) + m.altitude/6378140*math.Sin(latitude)
	parallax := math.Sin(sample.parallax)
	rightAscensionParallax := math.Atan2(-x*parallax*math.Sin(hourAngle), math.Cos(sample.declination)-x*parallax*math.Cos(hourAngle))
	declination := math.Atan2((math.Sin(sample.declination)-y*parallax)*math.Cos(rightAscensionParallax), math.Cos(sample.declination)-x*parallax*math.Cos(hourAngle))
	topocentricHourAngle := hourAngle - rightAscensionParallax
	elevation := math.Asin(math.Sin(latitude)*math.Sin(declination)+math.Cos(latitude)*math.Cos(declination)*math.Cos(topocentricHourAngle)) / radians
	if elevation >= -(0.26667 + 0.5667) {
		elevation += (m.pressure / 100 / 1010) * (283.0 / (273 + 12)) * 1.02 / (60 * math.Tan((elevation+10.3/(elevation+5.11))*radians))
	}
	return elevation
}
