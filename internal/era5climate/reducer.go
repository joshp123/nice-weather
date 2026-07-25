// Package era5climate reduces validated source-neutral ERA5 daily shards into
// bounded monthly climate results. It owns no scoring policy.
package era5climate

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"math"
	"sort"
	"time"

	"nice-weather-finder/internal/climate"
	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/providers/era5hourly"
)

const (
	defaultBatchCells = 32
	maximumBatchCells = 256
)

// Config selects one calendar month from the fixed complete 2006–2025 product
// reference window.
type Config struct {
	Month      time.Month
	BatchCells int
}

// Sink stages one complete month transaction. Add must not publish a result.
// Commit makes all staged results visible at once; Abort discards them.
type Sink interface {
	Add(CellResult) error
	Commit() error
	Abort() error
}

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

type cellAccumulator struct {
	observations []climate.DailyObservation
	expected     []time.Time
}

// ReduceMonth validates and reduces one shard for every year in the reference
// window. Results are emitted in cell-plan order. At most BatchCells cells and
// their daily evidence are retained at once.
func ReduceMonth(
	ctx context.Context,
	config Config,
	shards []*era5hourly.DailyShard,
	sink Sink,
) error {
	return reduceMonthWindow(ctx, climate.DefaultReferenceWindow(), config, shards, sink, nil)
}

func reduceMonthWindow(
	ctx context.Context,
	window climate.ReferenceWindow,
	config Config,
	shards []*era5hourly.DailyShard,
	sink Sink,
	observeBatch func(firstCell, cellCount int),
) (returnErr error) {
	if sink == nil {
		return errors.New("ERA5 climate reduction requires a transactional sink")
	}
	committed := false
	defer func() {
		if !committed {
			returnErr = errors.Join(returnErr, sink.Abort())
		}
	}()
	if err := stageMonth(ctx, window, config, shards, sink, observeBatch); err != nil {
		return err
	}
	for index, shard := range shards {
		if err := shard.VerifyPayload(ctx); err != nil {
			return fmt.Errorf("revalidate ERA5 climate year %d before commit: %w", window.FirstYear+index, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := sink.Commit(); err != nil {
		return fmt.Errorf("commit ERA5 climate month: %w", err)
	}
	committed = true
	return nil
}

func stageMonth(
	ctx context.Context,
	window climate.ReferenceWindow,
	config Config,
	shards []*era5hourly.DailyShard,
	sink Sink,
	observeBatch func(firstCell, cellCount int),
) error {
	if ctx == nil {
		return errors.New("ERA5 climate reduction requires a context")
	}
	if window.FirstYear < 2006 || window.LastYear > 2025 || window.FirstYear > window.LastYear {
		return fmt.Errorf("ERA5 climate reference window %d–%d is outside complete retained history", window.FirstYear, window.LastYear)
	}
	if config.Month < time.January || config.Month > time.December {
		return errors.New("ERA5 climate reduction requires a calendar month")
	}
	batchCells := config.BatchCells
	if batchCells == 0 {
		batchCells = defaultBatchCells
	}
	if batchCells < 1 || batchCells > maximumBatchCells {
		return fmt.Errorf("ERA5 climate batch has %d cells; want 1–%d", batchCells, maximumBatchCells)
	}
	wantShards := window.LastYear - window.FirstYear + 1
	if len(shards) != wantShards {
		return fmt.Errorf("ERA5 climate reduction has %d year shards; want %d for %d–%d", len(shards), wantShards, window.FirstYear, window.LastYear)
	}

	identities := make([]era5hourly.DailyShardIdentity, len(shards))
	for index, shard := range shards {
		if err := ctx.Err(); err != nil {
			return err
		}
		identity, err := shard.Identity()
		if err != nil {
			return fmt.Errorf("inspect ERA5 climate shard %d: %w", index, err)
		}
		wantYear := window.FirstYear + index
		if identity.Year != wantYear {
			return fmt.Errorf("ERA5 climate shard %d has year %d; want unique ordered year %d", index, identity.Year, wantYear)
		}
		if identity.Month != config.Month {
			return fmt.Errorf("ERA5 climate shard %d is %s; want %s", index, identity.Month, config.Month)
		}
		wantDays := time.Date(identity.Year, config.Month+1, 0, 0, 0, 0, 0, time.UTC).Day()
		if identity.Days != wantDays {
			return fmt.Errorf("ERA5 climate shard %d has %d calendar slots; want %d", index, identity.Days, wantDays)
		}
		if index > 0 {
			if identity.CellCount != identities[0].CellCount ||
				identity.PlanSHA256 != identities[0].PlanSHA256 {
				return fmt.Errorf("ERA5 climate shard %d uses a different ordered cell plan", index)
			}
		}
		identities[index] = identity
	}
	cellTotal := identities[0].CellCount
	consumed := make([]hash.Hash, len(shards))
	for index := range consumed {
		consumed[index] = sha256.New()
	}
	for firstCell := 0; firstCell < cellTotal; firstCell += batchCells {
		if err := ctx.Err(); err != nil {
			return err
		}
		cellCount := min(batchCells, cellTotal-firstCell)
		if observeBatch != nil {
			observeBatch(firstCell, cellCount)
		}
		accumulators := make([]cellAccumulator, cellCount)
		for shardIndex, shard := range shards {
			identity := identities[shardIndex]
			recordOffset := 0
			err := shard.WalkCellRange(firstCell, cellCount, consumed[shardIndex], func(day era5hourly.DailyMetrics, available bool) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				wantCell := firstCell + recordOffset/identity.Days
				wantDay := recordOffset%identity.Days + 1
				recordOffset++
				if day.CellIndex != wantCell || day.Date.Day() != wantDay ||
					day.Date.Year() != identity.Year || day.Date.Month() != identity.Month {
					return errors.New("ERA5 climate shard returned records out of cell-calendar order")
				}
				if !available {
					return nil
				}
				accumulator := &accumulators[day.CellIndex-firstCell]
				accumulator.expected = append(accumulator.expected, day.Date)
				accumulator.observations = append(accumulator.observations, climate.DailyObservation{
					Date: day.Date, Latitude: day.Latitude, Longitude: day.Longitude, Metrics: day.Metrics,
				})
				return nil
			})
			if err != nil {
				return fmt.Errorf("read ERA5 climate year %d cell batch %d–%d: %w", identity.Year, firstCell, firstCell+cellCount-1, err)
			}
			if recordOffset != cellCount*identity.Days {
				return fmt.Errorf("ERA5 climate year %d cell batch is incomplete", identity.Year)
			}
		}
		for relativeCell := range accumulators {
			if err := ctx.Err(); err != nil {
				return err
			}
			cellIndex := firstCell + relativeCell
			accumulator := accumulators[relativeCell]
			distribution, err := climate.AggregateObservations(accumulator.observations, accumulator.expected)
			if err != nil {
				return fmt.Errorf("reduce ERA5 climate cell %d: %w", cellIndex, err)
			}
			record, err := distribution.DeliveryRecord()
			if err != nil {
				return fmt.Errorf("deliver ERA5 climate cell %d: %w", cellIndex, err)
			}
			physical, err := summarizePhysical(accumulator.observations, record.BrightGoodPercent)
			if err != nil {
				return fmt.Errorf("summarize ERA5 climate cell %d: %w", cellIndex, err)
			}
			cell, err := shards[0].Cell(cellIndex)
			if err != nil {
				return fmt.Errorf("read ERA5 climate cell %d identity: %w", cellIndex, err)
			}
			if err := sink.Add(CellResult{
				CellIndex: cellIndex, Latitude: cell.Latitude, Longitude: cell.Longitude,
				Days: len(accumulator.observations), Distribution: record, Physical: physical,
			}); err != nil {
				return fmt.Errorf("stage ERA5 climate cell %d: %w", cellIndex, err)
			}
		}
	}
	for index, shard := range shards {
		if err := shard.VerifyConsumedPayload(consumed[index].Sum(nil)); err != nil {
			return fmt.Errorf("verify ERA5 climate year %d consumed payload: %w", window.FirstYear+index, err)
		}
	}
	return nil
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
