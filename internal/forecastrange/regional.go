package forecastrange

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"nice-weather-finder/internal/forecasttiles"
	"path/filepath"
	"sort"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/forecastcatalog"
	"nice-weather-finder/internal/tiledelivery"
)

func attachRegionalInputs(config Config, inputs []loadedInput) error {
	seen := make(map[string]bool)
	var run string
	for _, reference := range config.RegionalInputs {
		day := -1
		for index, input := range inputs {
			if input.reference.Date == reference.Date {
				day = index
				break
			}
		}
		expectedPath := filepath.ToSlash(filepath.Join("forecast-days", reference.Date, forecasttiles.TierEurope, forecastcatalogFilename))
		if day < 0 || day >= 5 || seen[reference.Date] || reference.Path != expectedPath {
			return errors.New("regional range input date or path is invalid")
		}
		seen[reference.Date] = true
		if err := rejectSymlinkPath(config.Root, reference.Path); err != nil {
			return err
		}
		payload, err := readRegularBounded(filepath.Join(config.Root, filepath.FromSlash(reference.Path)), forecastcatalog.MaximumCatalogBytes)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(payload)
		if uint64(len(payload)) != reference.Bytes || hex.EncodeToString(digest[:]) != reference.SHA256 {
			return errors.New("regional daily catalogue checksum mismatch")
		}
		catalog, err := forecastcatalog.Parse(payload)
		if err != nil {
			return err
		}
		if catalog.ReleaseID != inputs[0].catalog.ReleaseID || catalog.LocalDate != reference.Date || catalog.Provider != forecasttiles.ProviderICONEU || catalog.Tier != forecasttiles.TierEurope || catalog.Evidence.Product != forecastcatalog.ProductICONEU || catalog.Evidence.NativeResolutionKM != 7 || catalog.ScalarMaxZoom != RegionalMaxZoom {
			return errors.New("regional range input is not its complete ICON-EU daily product")
		}
		if run != "" && run != catalog.Evidence.RunUTC {
			return errors.New("regional range mixes ICON-EU runs")
		}
		run = catalog.Evidence.RunUTC
		points := make(map[tileKey]forecastcatalog.DailyPointBlock, len(catalog.DailyPointBlocks))
		for _, point := range catalog.DailyPointBlocks {
			points[tileKey{point.Zoom, point.X, point.Y}] = point
		}
		inputs[day].regional = &loadedInput{reference: reference, catalog: catalog, root: filepath.Dir(filepath.Join(config.Root, filepath.FromSlash(reference.Path))), points: points}
	}
	return nil
}

func inputTiles(inputs []loadedInput, zoom uint8) []tileKey {
	tiles := make(map[tileKey]bool)
	if zoom == MaxZoom {
		for y := uint32(0); y < 1<<zoom; y++ {
			for x := uint32(0); x < 1<<zoom; x++ {
				tiles[tileKey{zoom, x, y}] = true
			}
		}
	} else {
		for _, input := range inputs {
			if input.regional != nil {
				for key := range input.regional.points {
					tiles[tileKey{zoom, key.x / 4, key.y / 4}] = true
				}
			}
		}
	}
	result := make([]tileKey, 0, len(tiles))
	for key := range tiles {
		result = append(result, key)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].y != result[j].y {
			return result[i].y < result[j].y
		}
		return result[i].x < result[j].x
	})
	return result
}

func dailyTile(ctx context.Context, input loadedInput, x, y uint32, zoom uint8) ([]delivery.DailyPointRecord, uint64, error) {
	records := make([]delivery.DailyPointRecord, tiledelivery.TileSize*tiledelivery.TileSize)
	var bytes uint64
	for blockY := uint32(0); blockY < 4; blockY++ {
		for blockX := uint32(0); blockX < 4; blockX++ {
			if err := ctx.Err(); err != nil {
				return nil, 0, err
			}
			point, ok := input.points[tileKey{zoom, x*4 + blockX, y*4 + blockY}]
			if !ok {
				continue
			}
			if err := rejectSymlinkPath(input.root, point.Path); err != nil {
				return nil, 0, err
			}
			reader, err := tiledelivery.OpenPoint(filepath.Join(input.root, filepath.FromSlash(point.Path)))
			if err != nil {
				return nil, 0, err
			}
			meta := reader.Metadata()
			if meta.ReleaseID != input.catalog.ReleaseID || meta.Period != input.catalog.LocalDate || meta.ID.Tier != input.catalog.Tier || meta.ID.Zoom != point.Zoom || meta.ID.X != point.X || meta.ID.Y != point.Y || meta.Bytes != point.Bytes || meta.SHA256 != point.SHA256 || meta.PayloadSHA256 != point.PayloadSHA256 {
				_ = reader.Close()
				return nil, 0, errors.New("daily display block does not match its catalogue")
			}
			block, readErr := reader.Records()
			closeErr := reader.Close()
			if readErr != nil || closeErr != nil {
				return nil, 0, errors.Join(readErr, closeErr)
			}
			for row := 0; row < tiledelivery.PointBlockSize; row++ {
				destination := (int(blockY)*tiledelivery.PointBlockSize+row)*tiledelivery.TileSize + int(blockX)*tiledelivery.PointBlockSize
				copy(records[destination:destination+tiledelivery.PointBlockSize], block[row*tiledelivery.PointBlockSize:(row+1)*tiledelivery.PointBlockSize])
			}
			bytes += point.Bytes
		}
	}
	return records, bytes, nil
}

func buildTileRecordsAtZoom(ctx context.Context, inputs []loadedInput, x, y uint32, zoom uint8) ([]delivery.RangePointRecord, uint64, error) {
	var daily [delivery.ForecastPointDayCount][]delivery.DailyPointRecord
	var bytes uint64
	factor := uint32(1) << (zoom - MaxZoom)
	for day, input := range inputs {
		global, size, err := dailyTile(ctx, input, x/factor, y/factor, MaxZoom)
		if err != nil {
			return nil, 0, err
		}
		bytes += size
		if zoom == MaxZoom {
			daily[day] = global
			continue
		}
		daily[day] = make([]delivery.DailyPointRecord, len(global))
		for row := uint32(0); row < tiledelivery.TileSize; row++ {
			for column := uint32(0); column < tiledelivery.TileSize; column++ {
				globalRow := (y%factor*tiledelivery.TileSize + row) / factor
				globalColumn := (x%factor*tiledelivery.TileSize + column) / factor
				daily[day][row*tiledelivery.TileSize+column] = global[globalRow*tiledelivery.TileSize+globalColumn]
			}
		}
		if input.regional != nil {
			detail, size, err := dailyTile(ctx, *input.regional, x, y, zoom)
			if err != nil {
				return nil, 0, err
			}
			bytes += size
			for index, record := range detail {
				if record.Weather.Valid {
					daily[day][index] = record
				}
			}
		}
	}
	records := make([]delivery.RangePointRecord, tiledelivery.TileSize*tiledelivery.TileSize)
	for index := range records {
		if index%tiledelivery.TileSize == 0 {
			if err := ctx.Err(); err != nil {
				return nil, 0, err
			}
		}
		var displays [delivery.ForecastPointDayCount]delivery.DailyPointRecord
		complete := true
		for day := range displays {
			displays[day] = daily[day][index]
			if !displays[day].Weather.Valid {
				complete = false
				break
			}
		}
		if !complete {
			continue
		}
		record, err := rangeRecord(displays)
		if err != nil {
			return nil, 0, fmt.Errorf("summarize stored daily display record: %w", err)
		}
		records[index] = record
	}
	return records, bytes, nil
}
