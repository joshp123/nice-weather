package climatecanary

import (
	"context"
	"errors"
	"time"

	"nice-weather-finder/internal/climate"
	"nice-weather-finder/internal/climatebuild"
	"nice-weather-finder/internal/providers/agera5"
)

type syntheticSource struct {
	block     agera5.BlockCoordinate
	firstDate time.Time
	lastDate  time.Time
}

type syntheticReference struct {
	field     agera5.Field
	block     agera5.BlockCoordinate
	firstDate time.Time
	lastDate  time.Time
}

func (reference syntheticReference) Field() agera5.Field           { return reference.field }
func (reference syntheticReference) Block() agera5.BlockCoordinate { return reference.block }
func (reference syntheticReference) FirstDate() time.Time          { return reference.firstDate }
func (reference syntheticReference) LastDate() time.Time           { return reference.lastDate }

type syntheticBlock struct{ reference syntheticReference }

func (block syntheticBlock) Reference() climatebuild.ChunkReference { return block.reference }
func (block syntheticBlock) TimeCount() int {
	return int(block.reference.lastDate.Sub(block.reference.firstDate)/(24*time.Hour)) + 1
}
func (syntheticBlock) LatitudeCount() int  { return 1 }
func (syntheticBlock) LongitudeCount() int { return 1 }
func (block syntheticBlock) Date(offset int) (time.Time, error) {
	if offset < 0 || offset >= block.TimeCount() {
		return time.Time{}, errors.New("synthetic time offset outside block")
	}
	return block.reference.firstDate.AddDate(0, 0, offset), nil
}
func (block syntheticBlock) Location(latitudeOffset, longitudeOffset int) (float64, float64, error) {
	if latitudeOffset != 0 || longitudeOffset != 0 {
		return 0, 0, errors.New("synthetic location offset outside block")
	}
	return -90 + float64(block.reference.block.Latitude*16)/10, -180 + float64(block.reference.block.Longitude*32)/10, nil
}
func (block syntheticBlock) Value(timeOffset, latitudeOffset, longitudeOffset int) (float32, bool, error) {
	if timeOffset < 0 || timeOffset >= block.TimeCount() || latitudeOffset != 0 || longitudeOffset != 0 {
		return 0, false, errors.New("synthetic value offset outside block")
	}
	switch block.reference.field {
	case agera5.DaytimeMeanTemperature:
		return 297.15, true, nil
	case agera5.MinimumTemperature:
		return 291.15, true, nil
	case agera5.MaximumTemperature:
		return 301.15, true, nil
	case agera5.MeanDewPoint:
		return 285.15, true, nil
	case agera5.Precipitation, agera5.PrecipitationDuration:
		return 0, true, nil
	case agera5.SolarRadiation:
		return 20_000_000, true, nil
	case agera5.CloudCover:
		return .2, true, nil
	case agera5.MeanWindSpeed:
		return 3, true, nil
	default:
		return 0, false, errors.New("unknown synthetic AgERA5 field")
	}
}

func (source syntheticSource) Shape(agera5.BlockCoordinate) (int, int, error) { return 1, 1, nil }
func (source syntheticSource) Plan(firstDate, lastDate time.Time, block agera5.BlockCoordinate) ([]climatebuild.ChunkReference, error) {
	if block != source.block || !firstDate.Equal(source.firstDate) || !lastDate.Equal(source.lastDate) {
		return nil, errors.New("synthetic plan differs from configured scope")
	}
	fields := agera5.RequiredFields()
	references := make([]climatebuild.ChunkReference, len(fields))
	for index, field := range fields {
		references[index] = syntheticReference{field: field, block: block, firstDate: firstDate, lastDate: lastDate}
	}
	return references, nil
}
func (syntheticSource) Fetch(_ context.Context, reference climatebuild.ChunkReference) (climatebuild.Block, error) {
	value, ok := reference.(syntheticReference)
	if !ok {
		return nil, errors.New("foreign synthetic reference")
	}
	return syntheticBlock{reference: value}, nil
}

func executeSynthetic(ctx context.Context, _ string, block agera5.BlockCoordinate, window climate.ReferenceWindow, _ string) (execution, error) {
	firstDate := time.Date(window.FirstYear, time.January, 1, 0, 0, 0, 0, time.UTC)
	lastDate := time.Date(window.LastYear, time.December, 31, 0, 0, 0, 0, time.UTC)
	result, err := climatebuild.BuildSourceCanaryBlock(ctx, syntheticSource{block: block, firstDate: firstDate, lastDate: lastDate}, block, window)
	return execution{result: result}, err
}
