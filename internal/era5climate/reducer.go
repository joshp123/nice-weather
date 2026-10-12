// Package era5climate reduces complete ERA5 daily histories into monthly
// distributions and physical summaries through the canonical scorer.
package era5climate

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"nice-weather-finder/internal/climate"
	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/providers/era5hourly"
)

// PhysicalSummary retains source-neutral evidence that can explain the
// distribution without asking the browser to score or infer weather.
type PhysicalSummary struct {
	DaytimeTemperatureP10C float64
	DaytimeTemperatureP50C float64
	DaytimeTemperatureP90C float64
	DaytimeMinimumP50C     float64
	DaytimeMaximumP50C     float64
	DewPointP50C           float64
	DewPointP90C           float64
	// RelativeHumidityP50Percent is the median local daytime mean. It is
	// explanatory evidence only; dew point owns the humidity comfort policy.
	RelativeHumidityP50Percent float64
	// MuggyDayPercent is the share of local daytime means with a dew point at
	// or above 18°C. It is a daily share, not the fraction of hourly time.
	MuggyDayPercent      uint8
	RainFreeDayPercent   uint8
	RainP90MM            float64
	CloudCoverP50Percent float64
	ClearSkyDayPercent   uint8
	WindP50KPH           float64
	WindP90KPH           float64
}

// CellResult is one ordered cell result. Distribution and Physical are
// calculated from the same complete set of real local days.
type CellResult struct {
	CellIndex    int
	Latitude     float64
	Longitude    float64
	Days         int
	Distribution delivery.ClimateDistributionRecord
	Physical     PhysicalSummary
}

// ReduceCell uses the canonical daily scorer and physical quantiles for one
// complete monthly history. Annual-tile ingestion uses this path.
func ReduceCell(cellIndex int, cell era5hourly.Cell, observations []climate.DailyObservation, expected []time.Time) (CellResult, error) {
	distribution, err := climate.AggregateObservations(observations, expected)
	if err != nil {
		return CellResult{}, fmt.Errorf("reduce ERA5 climate cell %d: %w", cellIndex, err)
	}
	record, err := distribution.DeliveryRecord()
	if err != nil {
		return CellResult{}, err
	}
	physical, err := summarizePhysical(observations, record.BrightGoodPercent)
	if err != nil {
		return CellResult{}, err
	}
	return CellResult{CellIndex: cellIndex, Latitude: cell.Latitude, Longitude: cell.Longitude, Days: len(observations), Distribution: record, Physical: physical}, nil
}

func summarizePhysical(observations []climate.DailyObservation, clearSkyDayPercent uint8) (PhysicalSummary, error) {
	if len(observations) == 0 {
		return PhysicalSummary{}, errors.New("physical climate evidence is empty")
	}
	daytime := make([]float64, len(observations))
	minimum := make([]float64, len(observations))
	maximum := make([]float64, len(observations))
	dewPoint := make([]float64, len(observations))
	relativeHumidity := make([]float64, len(observations))
	rain := make([]float64, len(observations))
	cloud := make([]float64, len(observations))
	wind := make([]float64, len(observations))
	rainFree, muggy := 0, 0
	for index, observation := range observations {
		metrics := observation.Metrics
		if !metrics.DewPointC.Available || !metrics.RelativeHumidityPercent.Available ||
			!metrics.CloudCoverPercent.Available {
			return PhysicalSummary{}, fmt.Errorf("day %s lacks required humidity or cloud evidence", observation.Date.Format("2006-01-02"))
		}
		daytime[index] = metrics.ComfortableTempC
		minimum[index] = metrics.MinTempC
		maximum[index] = metrics.MaxTempC
		dewPoint[index] = metrics.DewPointC.Value
		relativeHumidity[index] = metrics.RelativeHumidityPercent.Value
		rain[index] = metrics.RainMM
		cloud[index] = metrics.CloudCoverPercent.Value
		wind[index] = metrics.WindSpeedKPH
		if metrics.RainMM == 0 {
			rainFree++
		}
		if metrics.DewPointC.Value >= 18 {
			muggy++
		}
	}
	return PhysicalSummary{
		DaytimeTemperatureP10C:     quantile(daytime, .10),
		DaytimeTemperatureP50C:     quantile(daytime, .50),
		DaytimeTemperatureP90C:     quantile(daytime, .90),
		DaytimeMinimumP50C:         quantile(minimum, .50),
		DaytimeMaximumP50C:         quantile(maximum, .50),
		DewPointP50C:               quantile(dewPoint, .50),
		DewPointP90C:               quantile(dewPoint, .90),
		RelativeHumidityP50Percent: quantile(relativeHumidity, .50),
		MuggyDayPercent:            percent(muggy, len(observations)),
		RainFreeDayPercent:         percent(rainFree, len(observations)),
		RainP90MM:                  quantile(rain, .90),
		CloudCoverP50Percent:       quantile(cloud, .50),
		ClearSkyDayPercent:         clearSkyDayPercent,
		WindP50KPH:                 quantile(wind, .50),
		WindP90KPH:                 quantile(wind, .90),
	}, nil
}

// quantile uses the inclusive linear sample quantile: p*(n-1), with exact
// interpolation between adjacent ordered observations.
func quantile(values []float64, probability float64) float64 {
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	position := probability * float64(len(ordered)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return ordered[lower]
	}
	fraction := position - float64(lower)
	return ordered[lower] + fraction*(ordered[upper]-ordered[lower])
}

func percent(count, total int) uint8 {
	return uint8(math.Floor(float64(count)*100/float64(total) + .5))
}
