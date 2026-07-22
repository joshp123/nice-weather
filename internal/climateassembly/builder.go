// Package climateassembly assembles one complete worldwide climate month from
// validated AgERA5 block checkpoints. It owns no checkpoint path or release
// publication policy.
package climateassembly

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"nice-weather-finder/internal/climatebuild"
	"nice-weather-finder/internal/climatetiles"
	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/providers/agera5"
	"nice-weather-finder/internal/tiledelivery"
)

const checkpointCacheEntries = 256

// CheckpointLoader resolves one exact checkpoint identity. The update
// orchestrator retains ownership of checkpoint paths, quarantine and retry.
type CheckpointLoader func(context.Context, climatebuild.CheckpointIdentity) (climatebuild.Checkpoint, error)

// Config describes one canonical calendar month inside a private climate
// publication stage.
type Config struct {
	Stage          climatetiles.Stage
	Period         string
	Month          time.Month
	Checkpoints    []climatebuild.CheckpointIdentity
	LoadCheckpoint CheckpointLoader
}

type monthBlock struct {
	identity climatebuild.CheckpointIdentity
	records  []delivery.ClimateDistributionRecord
}

type cacheEntry struct {
	block monthBlock
	item  *list.Element
}

// monthCache retains only one month from each loaded twelve-month checkpoint.
// Its fixed entry ceiling is independent of world size and tile count.
type monthCache struct {
	ctx           context.Context
	month         int
	loader        CheckpointLoader
	identities    map[agera5.BlockCoordinate]climatebuild.CheckpointIdentity
	expected      map[agera5.BlockCoordinate][sha256.Size]byte
	entries       map[agera5.BlockCoordinate]cacheEntry
	recent        *list.List
	peakEntries   int
	peakRecords   int
	qualityBlocks map[agera5.BlockCoordinate]struct{}
	sourceQuality climatetiles.SourceQuality
}

// BuildMonth writes one complete native point-block set and its seven-field
// z0-z4 scalar pyramid below config.Stage. The caller remains responsible for
// catalog.json, validation, installation and the publication pointer.
func BuildMonth(ctx context.Context, config Config) (climatetiles.Catalog, error) {
	provenance, identities, err := validateConfig(ctx, config)
	if err != nil {
		return climatetiles.Catalog{}, err
	}
	if err := ctx.Err(); err != nil {
		return climatetiles.Catalog{}, err
	}

	cache := newMonthCache(ctx, int(config.Month)-1, config.LoadCheckpoint, identities)
	pointBlocks, err := writePointBlocks(ctx, config, cache)
	if err != nil {
		return climatetiles.Catalog{}, err
	}
	if len(pointBlocks) != len(config.Checkpoints) {
		return climatetiles.Catalog{}, fmt.Errorf("climate month wrote %d point blocks for %d supplied checkpoint identities", len(pointBlocks), len(config.Checkpoints))
	}
	if len(cache.qualityBlocks) != len(config.Checkpoints) {
		return climatetiles.Catalog{}, fmt.Errorf("climate month measured source quality for %d of %d checkpoints", len(cache.qualityBlocks), len(config.Checkpoints))
	}

	writer, err := tiledelivery.NewWriter(config.Stage.Root, config.Stage.ReleaseID)
	if err != nil {
		return climatetiles.Catalog{}, err
	}
	fields := climatetiles.Fields()
	scalars, err := writeMaximumZoom(ctx, config, writer, cache, fields)
	if err != nil {
		return climatetiles.Catalog{}, err
	}
	scalars, err = writeLowerZooms(ctx, config, writer, fields, scalars)
	if err != nil {
		return climatetiles.Catalog{}, err
	}
	if err := verifyCompleteCoverage(config.Checkpoints, pointBlocks, fields, scalars); err != nil {
		return climatetiles.Catalog{}, err
	}
	if err := ctx.Err(); err != nil {
		return climatetiles.Catalog{}, err
	}
	catalog := climatetiles.NewCatalog(config.Stage.ReleaseID, config.Period, provenance, pointBlocks, scalars)
	catalog.SourceQuality = cache.sourceQuality
	return catalog, nil
}

func validateConfig(ctx context.Context, config Config) (climatetiles.Provenance, map[agera5.BlockCoordinate]climatebuild.CheckpointIdentity, error) {
	if ctx == nil {
		return climatetiles.Provenance{}, nil, errors.New("nil climate month context")
	}
	if config.Month < time.January || config.Month > time.December || config.Period != fmt.Sprintf("month-%02d", config.Month) {
		return climatetiles.Provenance{}, nil, errors.New("climate period and month are not one canonical month identity")
	}
	if config.LoadCheckpoint == nil || len(config.Checkpoints) == 0 {
		return climatetiles.Provenance{}, nil, errors.New("climate month requires checkpoint identities and a loader")
	}
	stage := config.Stage
	if stage.Root == "" || stage.ReleaseRoot == "" || stage.ReleaseID == "" || !filepath.IsAbs(stage.Root) || !filepath.IsAbs(stage.ReleaseRoot) || filepath.Clean(stage.Root) != stage.Root || filepath.Clean(stage.ReleaseRoot) != stage.ReleaseRoot {
		return climatetiles.Provenance{}, nil, errors.New("climate month requires a clean absolute private stage")
	}
	wantReleaseRoot := filepath.Join(stage.Root, "releases", stage.ReleaseID)
	if stage.ReleaseRoot != wantReleaseRoot {
		return climatetiles.Provenance{}, nil, errors.New("climate release root is outside its private stage")
	}
	stagePaths := []struct{ name, path string }{
		{"stage root", stage.Root},
		{"release namespace", filepath.Join(stage.Root, "releases")},
		{"release root", stage.ReleaseRoot},
	}
	for _, candidate := range stagePaths {
		name, path := candidate.name, candidate.path
		info, err := os.Lstat(path)
		if err != nil {
			return climatetiles.Provenance{}, nil, fmt.Errorf("stat climate %s: %w", name, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return climatetiles.Provenance{}, nil, fmt.Errorf("climate %s must be a real directory", name)
		}
	}
	entries, err := os.ReadDir(stage.ReleaseRoot)
	if err != nil {
		return climatetiles.Provenance{}, nil, err
	}
	if len(entries) != 0 {
		return climatetiles.Provenance{}, nil, errors.New("climate release stage is not empty")
	}
	if _, err := tiledelivery.NewWriter(stage.Root, stage.ReleaseID); err != nil {
		return climatetiles.Provenance{}, nil, err
	}

	identities := make(map[agera5.BlockCoordinate]climatebuild.CheckpointIdentity, len(config.Checkpoints))
	var sourceEvidence [sha256.Size]byte
	for index, identity := range config.Checkpoints {
		height, width, err := agera5.BlockShape(identity.Block)
		if err != nil || identity.Width != width || identity.Height != height || identity.NaturalEarthMaskSHA256 == ([sha256.Size]byte{}) || identity.SourceEvidenceSHA256 == ([sha256.Size]byte{}) {
			return climatetiles.Provenance{}, nil, fmt.Errorf("climate checkpoint identity %d is invalid", index)
		}
		if !blockIntersectsWorld(identity.Block) {
			return climatetiles.Provenance{}, nil, fmt.Errorf("climate checkpoint block %+v does not intersect the worldwide climate geometry", identity.Block)
		}
		if index == 0 {
			sourceEvidence = identity.SourceEvidenceSHA256
		} else {
			previous := config.Checkpoints[index-1]
			if previous.Block.Latitude < identity.Block.Latitude || (previous.Block.Latitude == identity.Block.Latitude && previous.Block.Longitude >= identity.Block.Longitude) {
				return climatetiles.Provenance{}, nil, errors.New("climate checkpoint identities are not in deterministic north-to-south, west-to-east order")
			}
			if identity.SourceEvidenceSHA256 != sourceEvidence {
				return climatetiles.Provenance{}, nil, errors.New("climate checkpoint identities contain different source evidence")
			}
		}
		if _, duplicate := identities[identity.Block]; duplicate {
			return climatetiles.Provenance{}, nil, fmt.Errorf("duplicate climate checkpoint block %+v", identity.Block)
		}
		identities[identity.Block] = identity
	}
	landSet := landMaskSetSHA256(config.Checkpoints)
	return climatetiles.Provenance{
		SourceEvidenceSHA256: hex.EncodeToString(sourceEvidence[:]),
		LandMaskSetSHA256:    hex.EncodeToString(landSet[:]),
		DerivationContract:   climatebuild.CheckpointDerivationContract,
	}, identities, nil
}

func blockIntersectsWorld(block agera5.BlockCoordinate) bool {
	height, width, err := agera5.BlockShape(block)
	if err != nil {
		return false
	}
	for latitudeOffset := range height {
		for longitudeOffset := range width {
			latitude, longitude, err := climatebuild.Locate(block, latitudeOffset, longitudeOffset)
			if err == nil {
				if _, included := climatebuild.WorldIndex(latitude, longitude); included {
					return true
				}
			}
		}
	}
	return false
}

func landMaskSetSHA256(identities []climatebuild.CheckpointIdentity) [sha256.Size]byte {
	digest := sha256.New()
	_, _ = digest.Write([]byte("nice-weather-climate-land-mask-set-v1\x00"))
	var encoded [8]byte
	for _, identity := range identities {
		binary.LittleEndian.PutUint16(encoded[0:2], uint16(identity.Block.Latitude))
		binary.LittleEndian.PutUint16(encoded[2:4], uint16(identity.Block.Longitude))
		binary.LittleEndian.PutUint16(encoded[4:6], uint16(identity.Width))
		binary.LittleEndian.PutUint16(encoded[6:8], uint16(identity.Height))
		_, _ = digest.Write(encoded[:])
		_, _ = digest.Write(identity.NaturalEarthMaskSHA256[:])
	}
	var result [sha256.Size]byte
	copy(result[:], digest.Sum(nil))
	return result
}

func newMonthCache(ctx context.Context, month int, loader CheckpointLoader, identities map[agera5.BlockCoordinate]climatebuild.CheckpointIdentity) *monthCache {
	return &monthCache{
		ctx: ctx, month: month, loader: loader, identities: identities,
		expected: make(map[agera5.BlockCoordinate][sha256.Size]byte, len(identities)),
		entries:  make(map[agera5.BlockCoordinate]cacheEntry, checkpointCacheEntries), recent: list.New(),
		qualityBlocks: make(map[agera5.BlockCoordinate]struct{}, len(identities)),
	}
}

func (cache *monthCache) get(block agera5.BlockCoordinate) (monthBlock, bool, error) {
	identity, exists := cache.identities[block]
	if !exists {
		return monthBlock{}, false, nil
	}
	if entry, ok := cache.entries[block]; ok {
		cache.recent.MoveToFront(entry.item)
		return entry.block, true, nil
	}
	if err := cache.ctx.Err(); err != nil {
		return monthBlock{}, false, err
	}
	checkpoint, err := cache.loader(cache.ctx, identity)
	if err != nil {
		return monthBlock{}, false, fmt.Errorf("load climate checkpoint block %+v: %w", block, err)
	}
	if checkpoint.Identity != identity {
		return monthBlock{}, false, fmt.Errorf("climate loader returned a different identity for block %+v", block)
	}
	if _, measured := cache.qualityBlocks[block]; !measured {
		repairs := uint64(checkpoint.ComfortTemperatureRepairs)
		if ^uint64(0)-cache.sourceQuality.ReferenceWindowMaterialDaytimeTemperatureRepairs < repairs {
			return monthBlock{}, false, errors.New("climate daytime-range repair count overflows catalogue telemetry")
		}
		cache.sourceQuality.ReferenceWindowMaterialDaytimeTemperatureRepairs += repairs
		cache.sourceQuality.MaximumReferenceWindowDaytimeAdjustmentC = math.Max(cache.sourceQuality.MaximumReferenceWindowDaytimeAdjustmentC, checkpoint.MaximumComfortTemperatureRepair)
		cache.qualityBlocks[block] = struct{}{}
	}
	records := checkpoint.Months[cache.month]
	if len(records) != identity.Width*identity.Height {
		return monthBlock{}, false, fmt.Errorf("climate checkpoint block %+v month has %d records; want %d", block, len(records), identity.Width*identity.Height)
	}
	records = append([]delivery.ClimateDistributionRecord(nil), records...)
	if err := validateMonthGeometry(identity, records); err != nil {
		return monthBlock{}, false, err
	}
	payloadDigest, err := climateMonthPayloadSHA256(records)
	if err != nil {
		return monthBlock{}, false, fmt.Errorf("validate climate checkpoint block %+v month: %w", block, err)
	}
	if expected, ok := cache.expected[block]; ok && payloadDigest != expected {
		return monthBlock{}, false, fmt.Errorf("climate checkpoint block %+v changed between point and scalar assembly", block)
	}
	loaded := monthBlock{identity: identity, records: records}
	item := cache.recent.PushFront(block)
	cache.entries[block] = cacheEntry{block: loaded, item: item}
	if len(cache.entries) > checkpointCacheEntries {
		oldest := cache.recent.Back()
		delete(cache.entries, oldest.Value.(agera5.BlockCoordinate))
		cache.recent.Remove(oldest)
	}
	cache.peakEntries = max(cache.peakEntries, len(cache.entries))
	recordCount := 0
	for _, entry := range cache.entries {
		recordCount += len(entry.block.records)
	}
	cache.peakRecords = max(cache.peakRecords, recordCount)
	return loaded, true, nil
}

func (cache *monthCache) register(block agera5.BlockCoordinate, digest [sha256.Size]byte) error {
	if prior, ok := cache.expected[block]; ok && prior != digest {
		return fmt.Errorf("climate point payload changed for block %+v", block)
	}
	cache.expected[block] = digest
	return nil
}

func validateMonthGeometry(identity climatebuild.CheckpointIdentity, records []delivery.ClimateDistributionRecord) error {
	for latitudeOffset := range identity.Height {
		for longitudeOffset := range identity.Width {
			index := latitudeOffset*identity.Width + longitudeOffset
			latitude, longitude, err := climatebuild.Locate(identity.Block, latitudeOffset, longitudeOffset)
			if err != nil {
				return err
			}
			if _, included := climatebuild.WorldIndex(latitude, longitude); !included && records[index].Valid {
				return fmt.Errorf("climate checkpoint block %+v publishes data outside the worldwide climate geometry", identity.Block)
			}
		}
	}
	return nil
}

func climateMonthPayloadSHA256(records []delivery.ClimateDistributionRecord) ([sha256.Size]byte, error) {
	digest := sha256.New()
	encoded := make([]byte, delivery.ClimateDistributionRecordBytes)
	for _, record := range records {
		if err := delivery.EncodeClimateDistributionRecord(encoded, record); err != nil {
			return [sha256.Size]byte{}, err
		}
		_, _ = digest.Write(encoded)
	}
	var result [sha256.Size]byte
	copy(result[:], digest.Sum(nil))
	return result, nil
}

func writePointBlocks(ctx context.Context, config Config, cache *monthCache) ([]climatetiles.PointBlock, error) {
	result := make([]climatetiles.PointBlock, 0, len(config.Checkpoints))
	for _, identity := range config.Checkpoints {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		block, found, err := cache.get(identity.Block)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("missing configured climate checkpoint block %+v", identity.Block)
		}
		path := filepath.Join(config.Stage.ReleaseRoot, filepath.FromSlash(climatetiles.PointBlockPath(config.Period, identity.Block)))
		asset, err := tiledelivery.WriteClimateBlock(path, config.Stage.ReleaseID, config.Period, identity.Block, block.records)
		if err != nil {
			return nil, fmt.Errorf("write climate point block %+v: %w", identity.Block, err)
		}
		if asset.Omitted {
			continue
		}
		digest, err := decodeSHA256(asset.PayloadSHA256)
		if err != nil {
			return nil, err
		}
		if err := cache.register(identity.Block, digest); err != nil {
			return nil, err
		}
		entry, err := climatetiles.PointBlockFromAsset(config.Stage.ReleaseRoot, asset)
		if err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, nil
}

func decodeSHA256(value string) ([sha256.Size]byte, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return [sha256.Size]byte{}, errors.New("asset returned an invalid SHA-256")
	}
	var result [sha256.Size]byte
	copy(result[:], decoded)
	return result, nil
}

func writeMaximumZoom(ctx context.Context, config Config, writer *tiledelivery.Writer, cache *monthCache, fields []climatetiles.Field) ([]climatetiles.ScalarTile, error) {
	count := uint32(1) << climatetiles.MaximumZoom
	result := make([]climatetiles.ScalarTile, 0, len(fields)*int(count)*int(count))
	candidates, err := maximumCandidates(config.Checkpoints)
	if err != nil {
		return nil, err
	}
	for y := uint32(0); y < count; y++ {
		for x := uint32(0); x < count; x++ {
			if _, candidate := candidates[[2]uint32{x, y}]; !candidate {
				continue
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			textures, hasData, err := maximumTextures(ctx, x, y, cache, fields)
			if err != nil {
				return nil, err
			}
			// A neighbouring tile's real gutter must not turn an all-water
			// interior into a published tile of its own.
			if !hasData {
				continue
			}
			for fieldIndex, field := range fields {
				id, err := tiledelivery.NewID(string(field), climatetiles.MaximumZoom, int64(x), int64(y))
				if err != nil {
					return nil, err
				}
				asset, err := writer.WriteScalar(id, config.Period, textureSampler(id, textures[fieldIndex]))
				if err != nil {
					return nil, fmt.Errorf("write climate scalar %s %d/%d/%d: %w", field, id.Zoom, id.X, id.Y, err)
				}
				if asset.Omitted {
					return nil, fmt.Errorf("climate scalar %s unexpectedly omitted for data tile %d/%d/%d", field, id.Zoom, id.X, id.Y)
				}
				if err := verifyMaximumTexture(asset, textures[fieldIndex]); err != nil {
					return nil, fmt.Errorf("verify climate point/scalar parity for %s %d/%d/%d: %w", field, id.Zoom, id.X, id.Y, err)
				}
				entry, err := climatetiles.ScalarTileFromAsset(config.Stage.ReleaseRoot, field, asset)
				if err != nil {
					return nil, err
				}
				result = append(result, entry)
			}
		}
	}
	return result, nil
}

// maximumCandidates converts checkpoint block envelopes to the small set of
// z4 interiors they can own. It is only an I/O bound: exact record validity is
// still decided for every texel by maximumTextures.
func maximumCandidates(identities []climatebuild.CheckpointIdentity) (map[[2]uint32]struct{}, error) {
	result := make(map[[2]uint32]struct{})
	for _, identity := range identities {
		west, east := math.Inf(1), math.Inf(-1)
		south, north := math.Inf(1), math.Inf(-1)
		for latitudeOffset := range identity.Height {
			for longitudeOffset := range identity.Width {
				sourceLatitude, sourceLongitude, err := climatebuild.Locate(identity.Block, latitudeOffset, longitudeOffset)
				if err != nil {
					return nil, err
				}
				if _, included := climatebuild.WorldIndex(sourceLatitude, sourceLongitude); !included {
					continue
				}
				latitude := -90 + float64(sourceLatitude)/10
				longitude := -180 + float64(sourceLongitude)/10
				west, east = min(west, longitude-.05), max(east, longitude+.05)
				south, north = min(south, latitude-.05), max(north, latitude+.05)
			}
		}
		if math.IsInf(west, 0) {
			continue
		}
		northWest, _, _, err := tiledelivery.At("climate-candidate", climatetiles.MaximumZoom, math.Nextafter(west, east), math.Nextafter(north, south))
		if err != nil {
			return nil, err
		}
		southEast, _, _, err := tiledelivery.At("climate-candidate", climatetiles.MaximumZoom, math.Nextafter(east, west), math.Nextafter(south, north))
		if err != nil {
			return nil, err
		}
		if northWest.X > southEast.X || northWest.Y > southEast.Y {
			return nil, fmt.Errorf("climate checkpoint block %+v produced a wrapped maximum-zoom envelope", identity.Block)
		}
		for y := northWest.Y; y <= southEast.Y; y++ {
			for x := northWest.X; x <= southEast.X; x++ {
				result[[2]uint32{x, y}] = struct{}{}
			}
		}
	}
	return result, nil
}

func maximumTextures(ctx context.Context, x, y uint32, cache *monthCache, fields []climatetiles.Field) ([][]byte, bool, error) {
	textures := make([][]byte, len(fields))
	for index := range textures {
		textures[index] = bytes.Repeat([]byte{tiledelivery.ScalarNoData}, tiledelivery.ScalarTileBytes)
	}
	worldPixels := int64(tiledelivery.TileSize) * (int64(1) << climatetiles.MaximumZoom)
	west := int64(x) * tiledelivery.TileSize
	north := int64(y) * tiledelivery.TileSize
	hasData := false
	for textureY := 0; textureY < tiledelivery.ScalarTextureSize; textureY++ {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		globalY := north + int64(textureY-tiledelivery.ScalarGutter)
		if globalY < 0 || globalY >= worldPixels {
			continue
		}
		for textureX := 0; textureX < tiledelivery.ScalarTextureSize; textureX++ {
			globalX := wrapPixel(west+int64(textureX-tiledelivery.ScalarGutter), worldPixels)
			longitude, latitude := mercatorTexelCentre(globalX, globalY, worldPixels)
			record, valid, err := cache.recordAt(longitude, latitude)
			if err != nil {
				return nil, false, err
			}
			if !valid {
				continue
			}
			position := textureY*tiledelivery.ScalarTextureSize + textureX
			for fieldIndex, field := range fields {
				value, err := fieldValue(record, field)
				if err != nil {
					return nil, false, err
				}
				textures[fieldIndex][position] = value
			}
			if textureX >= tiledelivery.ScalarGutter && textureX < tiledelivery.ScalarGutter+tiledelivery.TileSize && textureY >= tiledelivery.ScalarGutter && textureY < tiledelivery.ScalarGutter+tiledelivery.TileSize {
				hasData = true
			}
		}
	}
	return textures, hasData, nil
}

func (cache *monthCache) recordAt(longitude, latitude float64) (delivery.ClimateDistributionRecord, bool, error) {
	grid := climatebuild.WorldwideGridSpec()
	if longitude < grid.Bounds.West || longitude >= grid.Bounds.East || latitude < grid.Bounds.South || latitude > grid.Bounds.North {
		return delivery.ClimateDistributionRecord{}, false, nil
	}
	cellWidth := (grid.Bounds.East - grid.Bounds.West) / float64(grid.Cols)
	cellHeight := (grid.Bounds.North - grid.Bounds.South) / float64(grid.Rows)
	column := int(math.Floor((longitude - grid.Bounds.West) / cellWidth))
	row := int(math.Floor((grid.Bounds.North - latitude) / cellHeight))
	if row == int(grid.Rows) && latitude == grid.Bounds.South {
		row--
	}
	if row < 0 || row >= int(grid.Rows) || column < 0 || column >= int(grid.Cols) {
		return delivery.ClimateDistributionRecord{}, false, nil
	}
	sourceLatitude := 1750 - row
	sourceLongitude := 1 + column
	blockCoordinate := agera5.BlockCoordinate{Latitude: sourceLatitude / 16, Longitude: sourceLongitude / 32}
	block, found, err := cache.get(blockCoordinate)
	if err != nil || !found {
		return delivery.ClimateDistributionRecord{}, false, err
	}
	latitudeOffset := sourceLatitude - blockCoordinate.Latitude*16
	longitudeOffset := sourceLongitude - blockCoordinate.Longitude*32
	if latitudeOffset < 0 || latitudeOffset >= block.identity.Height || longitudeOffset < 0 || longitudeOffset >= block.identity.Width {
		return delivery.ClimateDistributionRecord{}, false, errors.New("climate source coordinate escaped its checkpoint block")
	}
	record := block.records[latitudeOffset*block.identity.Width+longitudeOffset]
	return record, record.Valid, nil
}

func fieldValue(record delivery.ClimateDistributionRecord, field climatetiles.Field) (uint8, error) {
	if !record.Valid {
		return 0, errors.New("cannot map an invalid climate distribution")
	}
	switch field {
	case climatetiles.FieldComfortGood:
		return record.ComfortGoodPercent, nil
	case climatetiles.FieldDryGood:
		return record.DryGoodPercent, nil
	case climatetiles.FieldBrightGood:
		return record.BrightGoodPercent, nil
	case climatetiles.FieldNiceGood:
		return record.Nice.GoodOrBetterPercent(), nil
	case climatetiles.FieldNiceCalmGood:
		return record.Nice.CalmGoodPercent, nil
	case climatetiles.FieldCyclingGood:
		return record.Cycling.GoodOrBetterPercent(), nil
	case climatetiles.FieldCyclingCalmGood:
		return record.Cycling.CalmGoodPercent, nil
	default:
		return 0, fmt.Errorf("unknown climate scalar field %q", field)
	}
}

func textureSampler(id tiledelivery.ID, texture []byte) tiledelivery.EvaluatedScoreSampler {
	west := int64(id.X)*tiledelivery.TileSize - tiledelivery.ScalarGutter
	north := int64(id.Y)*tiledelivery.TileSize - tiledelivery.ScalarGutter
	world := int64(tiledelivery.TileSize) * (int64(1) << id.Zoom)
	return func(sample tiledelivery.ScalarSample) (uint8, bool, error) {
		x := sample.GlobalX
		for x < west {
			x += world
		}
		for x >= west+tiledelivery.ScalarTextureSize {
			x -= world
		}
		column, row := int(x-west), int(sample.GlobalY-north)
		if row < 0 || column < 0 || row >= tiledelivery.ScalarTextureSize || column >= tiledelivery.ScalarTextureSize {
			return 0, false, errors.New("scalar writer requested a texel outside its climate texture")
		}
		value := texture[row*tiledelivery.ScalarTextureSize+column]
		return value, value != tiledelivery.ScalarNoData, nil
	}
}

func verifyMaximumTexture(asset tiledelivery.ScalarAsset, expected []byte) error {
	encoded, _, err := tiledelivery.ReadScalar(asset.Path, asset.SHA256)
	if err != nil {
		return err
	}
	if !bytes.Equal(encoded, expected) {
		return errors.New("maximum-zoom scalar does not equal its native point mapping")
	}
	return nil
}

func writeLowerZooms(ctx context.Context, config Config, writer *tiledelivery.Writer, fields []climatetiles.Field, scalars []climatetiles.ScalarTile) ([]climatetiles.ScalarTile, error) {
	levelAssets := make(map[tiledelivery.ID]tiledelivery.ScalarAsset)
	for _, scalar := range scalars {
		if scalar.Zoom != climatetiles.MaximumZoom {
			continue
		}
		id, _ := tiledelivery.NewID(string(scalar.Field), scalar.Zoom, int64(scalar.X), int64(scalar.Y))
		levelAssets[id] = scalarAsset(config, scalar, id)
	}
	for zoom := int(climatetiles.MaximumZoom) - 1; zoom >= 0; zoom-- {
		next := make(map[tiledelivery.ID]tiledelivery.ScalarAsset)
		count := uint32(1) << uint8(zoom)
		for y := uint32(0); y < count; y++ {
			for x := uint32(0); x < count; x++ {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				for _, field := range fields {
					id, err := tiledelivery.NewID(string(field), uint8(zoom), int64(x), int64(y))
					if err != nil {
						return nil, err
					}
					parent, err := tiledelivery.NewScalarParentSampler(id, func(id tiledelivery.ID) (tiledelivery.ScalarAsset, bool) {
						asset, ok := levelAssets[id]
						return asset, ok
					})
					if err != nil {
						return nil, err
					}
					if !parent.HasInteriorData() {
						continue
					}
					sample := func(sample tiledelivery.ScalarSample) (uint8, bool, error) {
						if err := ctx.Err(); err != nil {
							return 0, false, err
						}
						return parent.Sample(sample)
					}
					asset, err := writer.WriteScalar(id, config.Period, sample)
					if err != nil {
						return nil, fmt.Errorf("write lower climate scalar %s %d/%d/%d: %w", field, id.Zoom, id.X, id.Y, err)
					}
					if asset.Omitted {
						return nil, fmt.Errorf("lower climate scalar %s %d/%d/%d lost child data", field, id.Zoom, id.X, id.Y)
					}
					next[id] = asset
					entry, err := climatetiles.ScalarTileFromAsset(config.Stage.ReleaseRoot, field, asset)
					if err != nil {
						return nil, err
					}
					scalars = append(scalars, entry)
				}
			}
		}
		levelAssets = next
	}
	return scalars, nil
}

func scalarAsset(config Config, scalar climatetiles.ScalarTile, id tiledelivery.ID) tiledelivery.ScalarAsset {
	return tiledelivery.ScalarAsset{
		ID: id, ReleaseID: config.Stage.ReleaseID, Period: config.Period,
		Path:  filepath.Join(config.Stage.ReleaseRoot, filepath.FromSlash(scalar.Path)),
		Bytes: scalar.Bytes, SHA256: scalar.SHA256, MediaType: scalar.MediaType,
		DataTexels: scalar.DataTexels, NoDataTexels: scalar.NoDataTexels,
	}
}

func verifyCompleteCoverage(identities []climatebuild.CheckpointIdentity, points []climatetiles.PointBlock, fields []climatetiles.Field, scalars []climatetiles.ScalarTile) error {
	pointSet := make(map[agera5.BlockCoordinate]struct{}, len(points))
	for _, point := range points {
		pointSet[agera5.BlockCoordinate{Latitude: point.LatitudeBlock, Longitude: point.LongitudeBlock}] = struct{}{}
	}
	if len(pointSet) != len(identities) {
		return errors.New("climate point blocks do not cover every supplied checkpoint exactly once")
	}
	for _, identity := range identities {
		if _, ok := pointSet[identity.Block]; !ok {
			return fmt.Errorf("climate point blocks omit supplied checkpoint %+v", identity.Block)
		}
	}
	type coordinate struct {
		zoom uint8
		x, y uint32
	}
	coverage := make(map[climatetiles.Field]map[coordinate]struct{}, len(fields))
	for _, field := range fields {
		coverage[field] = make(map[coordinate]struct{})
	}
	for _, scalar := range scalars {
		set, ok := coverage[scalar.Field]
		if !ok {
			return fmt.Errorf("unknown climate scalar field %q", scalar.Field)
		}
		key := coordinate{zoom: scalar.Zoom, x: scalar.X, y: scalar.Y}
		if _, duplicate := set[key]; duplicate {
			return fmt.Errorf("duplicate climate scalar %s %d/%d/%d", scalar.Field, scalar.Zoom, scalar.X, scalar.Y)
		}
		set[key] = struct{}{}
	}
	if len(fields) == 0 {
		return errors.New("climate scalar field contract is empty")
	}
	baseline := coverage[fields[0]]
	if len(baseline) == 0 {
		return errors.New("climate month has no scalar data")
	}
	for _, field := range fields[1:] {
		if !sameCoordinates(baseline, coverage[field]) {
			return fmt.Errorf("climate scalar field %s has incomplete coordinate coverage", field)
		}
	}
	for zoom := uint8(0); zoom <= climatetiles.MaximumZoom; zoom++ {
		found := false
		for key := range baseline {
			found = found || key.zoom == zoom
		}
		if !found {
			return fmt.Errorf("climate scalar pyramid has no zoom %d data", zoom)
		}
	}
	if _, ok := baseline[coordinate{}]; !ok {
		return errors.New("climate scalar pyramid has no worldwide zoom 0 tile")
	}
	for key := range baseline {
		if key.zoom > 0 {
			if _, ok := baseline[coordinate{zoom: key.zoom - 1, x: key.x / 2, y: key.y / 2}]; !ok {
				return fmt.Errorf("climate scalar %d/%d/%d has no parent", key.zoom, key.x, key.y)
			}
		}
	}
	return nil
}

func sameCoordinates[T comparable](left, right map[T]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for key := range left {
		if _, ok := right[key]; !ok {
			return false
		}
	}
	return true
}

func wrapPixel(value, worldPixels int64) int64 {
	value %= worldPixels
	if value < 0 {
		value += worldPixels
	}
	return value
}

func mercatorTexelCentre(globalX, globalY, worldPixels int64) (float64, float64) {
	x := (float64(globalX) + 0.5) / float64(worldPixels)
	y := (float64(globalY) + 0.5) / float64(worldPixels)
	return x*360 - 180, math.Atan(math.Sinh(math.Pi*(1-2*y))) * 180 / math.Pi
}
