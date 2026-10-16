// Package clearsky implements the measured ERA5 sunshine estimator. Radiation
// fields describe the preceding hour; daylight uses a geometric horizon.
package clearsky

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"io"
	"math"
	"sync"
	"time"
)

//go:embed linke-quarter-degree.bin.gz
var compressedLinke []byte
var linkeOnce sync.Once
var linke []byte
var linkeError error

// Model holds the local atmospheric and terrain inputs, with no weather taste.
type Model struct {
	latitude, longitude, altitude, pressure float64
	monthly                                 [12]float64
}

// New uses pvlib's monthly Linke climatology sampled on the global 0.25° grid.
func New(latitude, longitude, altitude float64) (*Model, error) {
	linkeOnce.Do(func() {
		var reader *gzip.Reader
		reader, linkeError = gzip.NewReader(bytes.NewReader(compressedLinke))
		if linkeError != nil {
			return
		}
		defer reader.Close()
		linke, linkeError = io.ReadAll(reader)
		if linkeError == nil && len(linke) != 721*1440*12 {
			linkeError = fmt.Errorf("Linke grid has %d bytes", len(linke))
		}
	})
	if linkeError != nil {
		return nil, linkeError
	}
	if latitude < -90 || latitude > 90 || longitude < -180 || longitude > 180 {
		return nil, fmt.Errorf("coordinates outside Earth: %g,%g", latitude, longitude)
	}
	row := int(math.Round((90 - latitude) * 4))
	column := int(math.Round(positiveDegrees(longitude)*4)) % 1440
	var monthly [12]float64
	for month := range monthly {
		monthly[month] = float64(linke[(row*1440+column)*12+month]) / 20
	}
	return NewWithMonthlyTurbidity(latitude, longitude, altitude, monthly), nil
}

// NewWithMonthlyTurbidity allows a finer source climatology for off-grid places.
// Values are Linke turbidity, rather than the source file's scaled integers.
func NewWithMonthlyTurbidity(latitude, longitude, altitude float64, monthly [12]float64) *Model {
	return &Model{latitude: latitude, longitude: longitude, altitude: altitude, pressure: 100 * math.Pow((44331.514-altitude)/11880.516, 1/0.1902632), monthly: monthly}
}

type solarSample struct {
	rightAscension, declination, sidereal, parallax float64
	dayOfYear                                       int
	year                                            int
}

// Hour is the location-independent ephemeris for four midpoint samples. Reuse it
// across every cell in a tile before advancing to the next UTC hour.
type Hour struct {
	ending  time.Time
	samples [4]solarSample
}

func PrepareHour(ending time.Time) Hour {
	h := Hour{ending: ending.UTC()}
	for i := range h.samples {
		h.samples[i] = prepareSolarSample(h.ending.Add(-time.Duration(450+i*900) * time.Second))
	}
	return h
}
func (m *Model) HourEnding(ending time.Time) (horizontalWm2, normalWm2, daylightHours float64) {
	return m.Hour(PrepareHour(ending))
}
func (m *Model) Hour(hour Hour) (horizontalWm2, normalWm2, daylightHours float64) {
	for _, sample := range hour.samples {
		elevation := m.apparentElevation(sample)
		if elevation <= 0 {
			continue
		}
		cosineZenith := math.Sin(elevation * math.Pi / 180)
		zenith := 90 - elevation
		airmass := m.pressure / 101325 / (cosineZenith + 0.50572*math.Pow(6.07995+90-zenith, -1.6364))
		turbidity := m.turbidity(sample.year, sample.dayOfYear)
		firstHeight := math.Exp(-m.altitude / 8000)
		secondHeight := math.Exp(-m.altitude / 1250)
		firstCoefficient := 5.09e-5*m.altitude + 0.868
		secondCoefficient := 3.92e-5*m.altitude + 0.0387
		angle := 2 * math.Pi / 365 * float64(sample.dayOfYear-1)
		extraterrestrial := 1366.1 * (1.00011 + 0.034221*math.Cos(angle) + 0.00128*math.Sin(angle) + 0.000719*math.Cos(2*angle) + 7.7e-5*math.Sin(2*angle))
		global := firstCoefficient * extraterrestrial * cosineZenith * math.Exp(-secondCoefficient*airmass*(firstHeight+secondHeight*(turbidity-1)))
		beamOne := extraterrestrial * (0.664 + 0.163/firstHeight) * math.Exp(-0.09*airmass*(turbidity-1))
		beamTwo := global * (1 - (0.1-0.2*math.Exp(-turbidity))/(0.1+0.882/firstHeight)) / cosineZenith
		normal := math.Min(beamOne, beamTwo)
		normalWm2 += normal / 4
		horizontalWm2 += normal * cosineZenith / 4
	}
	return horizontalWm2, normalWm2, m.daylightOverlap(hour.ending)
}

func SunshineHours(directWm2, horizontalWm2, normalWm2, daylightHours float64) float64 {
	if normalWm2 < 120 || horizontalWm2 <= 0 {
		return 0
	}
	transmission := math.Max(0, math.Min(1, directWm2/math.Max(horizontalWm2, 0.001)))
	return daylightHours * math.Max(0, math.Min(1, (transmission-0.1)/0.9))
}

func (m *Model) turbidity(year, dayOfYear int) float64 {
	days := [12]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	if year%4 == 0 && (year%100 != 0 || year%400 == 0) {
		days[1] = 29
	}
	previousMiddle := -15.5
	previousValue := m.monthly[11]
	elapsed := 0
	for month, count := range days {
		middle := float64(elapsed) + float64(count)/2
		if float64(dayOfYear) <= middle {
			return previousValue + (m.monthly[month]-previousValue)*(float64(dayOfYear)-previousMiddle)/(middle-previousMiddle)
		}
		previousMiddle = middle
		previousValue = m.monthly[month]
		elapsed += count
	}
	return previousValue + (m.monthly[0]-previousValue)*(float64(dayOfYear)-previousMiddle)/(float64(elapsed)+15.5-previousMiddle)
}

func (m *Model) daylightOverlap(ending time.Time) float64 {
	angle := 2 * math.Pi / 365 * float64(ending.YearDay()-1)
	declination := 0.006918 - 0.399912*math.Cos(angle) + 0.070257*math.Sin(angle) - 0.006758*math.Cos(2*angle) + 0.000907*math.Sin(2*angle) - 0.002697*math.Cos(3*angle) + 0.00148*math.Sin(3*angle)
	equation := 229.18 * (0.000075 + 0.001868*math.Cos(angle) - 0.032077*math.Sin(angle) - 0.014615*math.Cos(2*angle) - 0.040849*math.Sin(2*angle))
	hourAngle := math.Acos(math.Max(-1, math.Min(1, -math.Tan(m.latitude*math.Pi/180)*math.Tan(declination)))) * 180 / math.Pi / 15
	noon := 12 - m.longitude/15 - equation/60
	low := noon - hourAngle
	high := noon + hourAngle
	hour := float64(ending.Hour()) + float64(ending.Minute())/60
	daylight := 0.0
	for _, offset := range []float64{-24, 0, 24} {
		daylight += math.Max(0, math.Min(hour, high+offset)-math.Max(hour-1, low+offset))
	}
	return daylight
}

func positiveDegrees(value float64) float64 { return math.Mod(math.Mod(value, 360)+360, 360) }
