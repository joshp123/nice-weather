package dwd

import (
	"context"
	"fmt"
	"time"

	"nice-weather-finder/internal/weatherinput"
)

const (
	WindowStartHour = 10
	WindowEndHour   = 20

	latestSameDayRunHourUTC = 6
)

type TimezoneAt func(lon, lat float64) (string, *time.Location, error)

type Horizon struct {
	ID        string
	LocalDate string
	Label     string
}

type ForecastGrid struct {
	Grid     weatherinput.Grid
	Horizons []Horizon
}

func ForecastSteps() []int {
	steps := make([]int, 0, 93)
	for step := 0; step <= 78; step++ {
		steps = append(steps, step)
	}
	for step := 81; step <= 120; step += 3 {
		steps = append(steps, step)
	}
	return steps
}

func HorizonDates(start time.Time, days int) []Horizon {
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	horizons := make([]Horizon, 0, days)
	for i := 0; i < days; i++ {
		date := start.AddDate(0, 0, i)
		id := date.Format(time.DateOnly)
		label := date.Format("Mon 2 Jan")
		horizons = append(horizons, Horizon{ID: id, LocalDate: id, Label: label})
	}
	return horizons
}

func FullLocalHorizonDates(runStartedAt time.Time, days int) []Horizon {
	start := runStartedAt.UTC()
	if start.Hour() > latestSameDayRunHourUTC {
		start = start.AddDate(0, 0, 1)
	}
	return HorizonDates(start, days)
}

func LoadLocalDaytimeGrid(ctx context.Context, dir string, runStartedAt time.Time, horizons []Horizon, timezoneAt TimezoneAt) (ForecastGrid, error) {
	grid, err := loadLocalDaytimeGrid(ctx, dir, runStartedAt, ForecastSteps(), horizons, timezoneAt, ReadGribValues)
	return ForecastGrid{Grid: grid, Horizons: horizons}, err
}

func loadLocalDaytimeGrid(
	ctx context.Context,
	dir string,
	runStartedAt time.Time,
	steps []int,
	horizons []Horizon,
	timezoneAt TimezoneAt,
	read gribReader,
) (weatherinput.Grid, error) {
	if len(steps) == 0 {
		return weatherinput.Grid{}, fmt.Errorf("DWD forecast grid needs at least one forecast step")
	}
	if len(horizons) == 0 {
		return weatherinput.Grid{}, fmt.Errorf("DWD forecast grid needs at least one horizon")
	}

	dateIndex := map[string]int{}
	for i, horizon := range horizons {
		dateIndex[horizon.LocalDate] = i
	}

	var grid weatherinput.Grid
	var meta gridMeta
	var accumulators []dayAccumulator
	var locations []*time.Location

	for stepIndex, step := range steps {
		values, err := loadStepFields(ctx, dir, step, read)
		if err != nil {
			return weatherinput.Grid{}, err
		}
		t2m := values["t_2m"]
		if len(t2m) < 2 {
			return weatherinput.Grid{}, fmt.Errorf("DWD T_2M grid is too small")
		}
		if err := validateAlignedFields(values, t2m); err != nil {
			return weatherinput.Grid{}, err
		}
		stepMeta, err := inferGrid(t2m)
		if err != nil {
			return weatherinput.Grid{}, err
		}
		if stepIndex == 0 {
			meta = stepMeta
			grid = weatherinput.NewGrid(meta.West, meta.East, meta.South, meta.North, meta.StepDeg)
			locations, err = cellLocations(grid, timezoneAt)
			if err != nil {
				return weatherinput.Grid{}, err
			}
			accumulators = make([]dayAccumulator, len(horizons))
			for i := range accumulators {
				accumulators[i] = newDayAccumulator(grid.Width * grid.Height)
			}
		} else if stepMeta != meta {
			return weatherinput.Grid{}, fmt.Errorf("DWD step %03d grid metadata changed: %+v != %+v", step, stepMeta, meta)
		}

		validAt := runStartedAt.Add(time.Duration(step) * time.Hour)
		for i := range t2m {
			local := validAt.In(locations[i])
			horizonIndex, ok := dateIndex[local.Format(time.DateOnly)]
			if !ok {
				continue
			}
			input := MapICONToWeatherInput(IconEUFields{
				T2MKelvin: values["t_2m"][i].Value,
				RelHum2M:  values["relhum_2m"][i].Value,
				TotPrecMM: values["tot_prec"][i].Value,
				CloudPct:  values["clct"][i].Value,
				U10MMS:    values["u_10m"][i].Value,
				V10MMS:    values["v_10m"][i].Value,
			})
			minute := local.Hour()*60 + local.Minute()
			accumulators[horizonIndex].Add(i, minute, input)
		}
	}

	for i, horizon := range horizons {
		values, valid := accumulators[i].Finish()
		grid.Horizons[horizon.ID] = values
		grid.Valid[horizon.ID] = valid
	}
	return grid, nil
}

func cellLocations(grid weatherinput.Grid, timezoneAt TimezoneAt) ([]*time.Location, error) {
	locations := make([]*time.Location, grid.Width*grid.Height)
	for y := 0; y < grid.Height; y++ {
		lat := grid.South + float64(y)*grid.StepDeg
		for x := 0; x < grid.Width; x++ {
			lon := grid.West + float64(x)*grid.StepDeg
			_, location, err := timezoneAt(lon, lat)
			if err != nil {
				return nil, err
			}
			locations[y*grid.Width+x] = location
		}
	}
	return locations, nil
}

type dayAccumulator struct {
	cells []dayCell
}

type dayCell struct {
	tempSum      float64
	humiditySum  float64
	cloudSum     float64
	windSum      float64
	count        int
	rainStart    float64
	rainEnd      float64
	startMinute  int
	endMinute    int
	hasRainStart bool
	hasRainEnd   bool
}

func newDayAccumulator(size int) dayAccumulator {
	cells := make([]dayCell, size)
	for i := range cells {
		cells[i].startMinute = -1
		cells[i].endMinute = 24*60 + 1
	}
	return dayAccumulator{cells: cells}
}

func (acc dayAccumulator) Add(index, minute int, input weatherinput.WeatherInput) {
	cell := &acc.cells[index]
	if minute >= WindowStartHour*60 && minute <= WindowEndHour*60 {
		cell.tempSum += input.TempC
		cell.humiditySum += input.Humidity
		cell.cloudSum += input.CloudPct
		cell.windSum += input.WindKMH
		cell.count++
	}
	if minute <= WindowStartHour*60 && (!cell.hasRainStart || minute > cell.startMinute) {
		cell.rainStart = input.RainMM
		cell.startMinute = minute
		cell.hasRainStart = true
	}
	if minute >= WindowEndHour*60 && (!cell.hasRainEnd || minute < cell.endMinute) {
		cell.rainEnd = input.RainMM
		cell.endMinute = minute
		cell.hasRainEnd = true
	}
}

func (acc dayAccumulator) Finish() ([]weatherinput.WeatherInput, []bool) {
	values := make([]weatherinput.WeatherInput, len(acc.cells))
	valid := make([]bool, len(acc.cells))
	for i, cell := range acc.cells {
		if cell.count == 0 || !cell.hasRainStart || !cell.hasRainEnd {
			continue
		}
		rain := cell.rainEnd - cell.rainStart
		if rain < 0 {
			rain = 0
		}
		count := float64(cell.count)
		values[i] = weatherinput.WeatherInput{
			TempC:    cell.tempSum / count,
			Humidity: cell.humiditySum / count,
			RainMM:   rain,
			CloudPct: cell.cloudSum / count,
			WindKMH:  cell.windSum / count,
		}
		valid[i] = true
	}
	return values, valid
}
