package destinations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/forecastpoints"
	"nice-weather-finder/internal/forecastrange"
	"nice-weather-finder/internal/forecastruns"
	"nice-weather-finder/internal/places"
	"nice-weather-finder/internal/tiledelivery"
)

const WorldwideSchema = "nice-best-weather-world-v1"

// WorldwideArtifact is the small static answer to “where should I go?”. Its
// scores come directly from the immutable worldwide forecast-range records.
// The browser only displays them and uses the coordinates to open the existing
// point sheet.
type WorldwideArtifact struct {
	Schema          string              `json:"schema"`
	ForecastIndexID string              `json:"forecastIndexId"`
	PlaceReleaseID  string              `json:"placeReleaseId"`
	StartDate       string              `json:"startDate"`
	EndDate         string              `json:"endDate"`
	ForecastRunUTC  string              `json:"forecastRunUtc"`
	Profiles        map[string][]Result `json:"profiles"`
}

type WorldwidePointer struct {
	Schema          string `json:"schema"`
	ForecastIndexID string `json:"forecastIndexId"`
	PlaceReleaseID  string `json:"placeReleaseId"`
	URL             string `json:"url"`
	SHA256          string `json:"sha256"`
}

func (WorldwidePointer) publicationPointer() {}

type WorldwideBuildSummary struct {
	ScannedPlaces int
	Bytes         int
	Elapsed       time.Duration
	Pointer       WorldwidePointer
}

// BuildWorldwideCurrent evaluates every validated settlement against the one
// canonical worldwide 14-day range and rotates the tiny pointer last. It runs
// during data publication, never on a public request.
func BuildWorldwideCurrent(ctx context.Context, artifactRoot string) (WorldwideBuildSummary, error) {
	started := time.Now()
	if ctx == nil {
		return WorldwideBuildSummary{}, errors.New("worldwide best weather requires a context")
	}
	absoluteRoot, err := filepath.Abs(artifactRoot)
	if err != nil {
		return WorldwideBuildSummary{}, err
	}
	pointer, index, _, err := forecastruns.LoadCurrentMetadata(absoluteRoot)
	if err != nil {
		return WorldwideBuildSummary{}, fmt.Errorf("load current worldwide forecast: %w", err)
	}
	if pointer.Current.IndexID != index.IndexID {
		return WorldwideBuildSummary{}, errors.New("worldwide forecast pointer and index disagree")
	}
	rangeRoot := filepath.Join(absoluteRoot, "forecast", filepath.FromSlash(filepath.Dir(index.Range.CatalogPath)))
	catalog, catalogPayload, err := forecastrange.ValidateInstalled(rangeRoot)
	if err != nil {
		return WorldwideBuildSummary{}, fmt.Errorf("validate worldwide forecast range: %w", err)
	}
	dates := make([]string, len(index.Days))
	for day := range index.Days {
		dates[day] = index.Days[day].Date
	}
	if err := validateWorldwideRange(index, catalog, catalogPayload, dates); err != nil {
		return WorldwideBuildSummary{}, err
	}

	lookup := newWorldwideLookup(ctx, rangeRoot, catalog)
	var placePointer places.Pointer
	scanned := 0
	profiles, err := EvaluateWorldwide(func(visit func(places.Candidate) error) error {
		var walkErr error
		placePointer, walkErr = places.WalkCurrent(absoluteRoot, func(item places.Candidate) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			scanned++
			return visit(item)
		})
		return walkErr
	}, lookup.lookup)
	if err != nil {
		return WorldwideBuildSummary{}, err
	}
	payload, err := json.Marshal(WorldwideArtifact{
		Schema: WorldwideSchema, ForecastIndexID: index.IndexID, PlaceReleaseID: placePointer.ReleaseID,
		StartDate: index.FirstDate, EndDate: index.LastDate, ForecastRunUTC: catalog.Source.RunUTC, Profiles: profiles,
	})
	if err != nil {
		return WorldwideBuildSummary{}, err
	}
	if len(payload) > 32*1024 {
		return WorldwideBuildSummary{}, fmt.Errorf("worldwide best-weather artifact has %d bytes; limit is 32768", len(payload))
	}
	digest := sha256.Sum256(payload)
	checksum := hex.EncodeToString(digest[:])
	relative := filepath.ToSlash(filepath.Join("best-weather", "releases", index.IndexID+"-world-"+checksum[:16]+".json"))
	worldPointer := WorldwidePointer{
		Schema: WorldwideSchema, ForecastIndexID: index.IndexID, PlaceReleaseID: placePointer.ReleaseID,
		URL: "/" + relative, SHA256: checksum,
	}
	if err := sourcesStillCurrent(absoluteRoot, pointer, placePointer); err != nil {
		return WorldwideBuildSummary{}, err
	}
	if err := publish(absoluteRoot, relative, payload, worldPointer); err != nil {
		return WorldwideBuildSummary{}, err
	}
	return WorldwideBuildSummary{ScannedPlaces: scanned, Bytes: len(payload), Elapsed: time.Since(started), Pointer: worldPointer}, nil
}

func validateWorldwideRange(index forecastruns.Index, catalog forecastrange.Catalog, payload []byte, dates []string) error {
	digest := sha256.Sum256(payload)
	if uint64(len(payload)) != index.Range.CatalogBytes || hex.EncodeToString(digest[:]) != index.Range.CatalogSHA256 {
		return errors.New("worldwide forecast range does not match its current index checksum")
	}
	if catalog.ReleaseID != index.IndexID || catalog.Source.RunUTC != index.Range.RunUTC || !slices.Equal(catalog.Dates, dates) {
		return errors.New("worldwide forecast range does not match its current index")
	}
	return nil
}

func sourcesStillCurrent(artifactRoot string, forecastPointer forecastruns.Pointer, placePointer places.Pointer) error {
	currentForecast, _, _, err := forecastruns.LoadCurrentMetadata(artifactRoot)
	if err != nil {
		return fmt.Errorf("recheck worldwide forecast pointer: %w", err)
	}
	currentPlaces, err := places.LoadCurrentPointer(artifactRoot)
	if err != nil {
		return fmt.Errorf("recheck place pointer: %w", err)
	}
	if currentForecast.Current != forecastPointer.Current || currentPlaces != placePointer {
		return errors.New("forecast or place publication changed while building best weather; retry against the current releases")
	}
	return nil
}

type worldwideLookup struct {
	ctx       context.Context
	root      string
	catalog   forecastrange.Catalog
	blocks    map[[2]uint32]*forecastrange.Block
	blockMeta map[[2]uint32]forecastrange.PointBlock
}

func newWorldwideLookup(ctx context.Context, root string, catalog forecastrange.Catalog) *worldwideLookup {
	metadata := make(map[[2]uint32]forecastrange.PointBlock, len(catalog.PointBlocks))
	for _, block := range catalog.PointBlocks {
		metadata[[2]uint32{block.Row, block.Column}] = block
	}
	return &worldwideLookup{ctx: ctx, root: root, catalog: catalog, blocks: make(map[[2]uint32]*forecastrange.Block), blockMeta: metadata}
}

func (reader *worldwideLookup) lookup(profile string, longitude, latitude float64) (delivery.RangeRecord, bool, error) {
	if err := reader.ctx.Err(); err != nil {
		return delivery.RangeRecord{}, false, err
	}
	id, row, column, err := tiledelivery.At(forecastpoints.TierGlobal, forecastrange.MaxZoom, longitude, latitude)
	if err != nil {
		return delivery.RangeRecord{}, false, nil
	}
	globalRow := id.Y*tiledelivery.TileSize + uint32(row)
	globalColumn := id.X*tiledelivery.TileSize + uint32(column)
	key := [2]uint32{globalRow / uint32(forecastrange.BlockRows), globalColumn / uint32(forecastrange.BlockColumns)}
	meta, exists := reader.blockMeta[key]
	if !exists {
		return delivery.RangeRecord{}, false, errors.New("worldwide range catalogue is missing a point block")
	}
	if meta.Omitted {
		return delivery.RangeRecord{}, false, nil
	}
	block := reader.blocks[key]
	if block == nil {
		identity, err := forecastrange.NewBlockIdentity(reader.catalog.ReleaseID, key[0], key[1])
		if err != nil {
			return delivery.RangeRecord{}, false, err
		}
		block, err = forecastrange.OpenBlock(filepath.Join(reader.root, filepath.FromSlash(meta.Path)), identity, meta.SHA256)
		if err != nil {
			return delivery.RangeRecord{}, false, fmt.Errorf("open worldwide range block %d/%d: %w", key[0], key[1], err)
		}
		reader.blocks[key] = block
	}
	record, valid, err := block.RecordAt(longitude, latitude)
	if err != nil || !valid {
		return delivery.RangeRecord{}, valid, err
	}
	switch profile {
	case delivery.ProfileNice:
		return record.Nice, true, nil
	case delivery.ProfileCycling:
		return record.Cycling, true, nil
	default:
		return delivery.RangeRecord{}, false, fmt.Errorf("unknown destination profile %q", profile)
	}
}
