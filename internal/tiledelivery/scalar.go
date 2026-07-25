package tiledelivery

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

const (
	// ScalarGutter is one real neighbouring texel on every edge. The WebGL
	// renderer interpolates these evaluated scores before it applies a palette.
	ScalarGutter      = 1
	ScalarTextureSize = TileSize + 2*ScalarGutter
	ScalarTileBytes   = ScalarTextureSize * ScalarTextureSize
	// ForecastScalarFallback marks display land whose detailed forecast tier
	// has no score. Generic and climate scalar APIs reject this reserved byte.
	ForecastScalarFallback = uint8(254)
	ScalarNoData           = uint8(255)
	ScalarMediaType        = "application/vnd.nice-weather.score-tile"
)

// ScalarSample identifies one Web Mercator texel centre. GlobalX wraps into
// the canonical world. GlobalY never wraps. Longitude is in [-180, 180), and
// Latitude is within the Web Mercator domain.
type ScalarSample struct {
	GlobalX   int64
	GlobalY   int64
	Longitude float64
	Latitude  float64
}

// EvaluatedScoreSampler returns a score already produced by the canonical Go
// scoring path. It is a spatial resampling seam, not a scoring policy.
type EvaluatedScoreSampler func(ScalarSample) (score uint8, valid bool, err error)

type ScalarStats struct {
	DataTexels   uint32
	NoDataTexels uint32
	DecodedBytes uint64
}

type ScalarAsset struct {
	ID           ID
	ReleaseID    string
	Period       string
	Path         string
	Bytes        uint64
	SHA256       string
	MediaType    string
	DataTexels   uint32
	NoDataTexels uint32
	Omitted      bool
}

// BuildScalarTile evaluates the 256x256 interior and its one-texel gutter.
// Interior texels are at indices [1, 256]. Gutter texels are copied by global
// pixel identity from the real adjacent tile; X wraps at the antimeridian and
// Y outside the Mercator world is no-data. The returned bytes are the exact
// R8 texture body expected by ScalarTileCanaryLayer after HTTP decoding.
func BuildScalarTile(id ID, sample EvaluatedScoreSampler) ([]byte, ScalarStats, error) {
	return buildScalarTile(id, sample, false)
}

// BuildForecastScalarTile accepts the forecast-detail fallback marker in
// addition to canonical scores and no-data.
func BuildForecastScalarTile(id ID, sample EvaluatedScoreSampler) ([]byte, ScalarStats, error) {
	return buildScalarTile(id, sample, true)
}

func buildScalarTile(id ID, sample EvaluatedScoreSampler, allowFallback bool) ([]byte, ScalarStats, error) {
	if err := id.validate(); err != nil {
		return nil, ScalarStats{}, err
	}
	if sample == nil {
		return nil, ScalarStats{}, errors.New("scalar score sampler is required")
	}

	worldPixels := int64(TileSize) * (int64(1) << id.Zoom)
	tileWest := int64(id.X) * TileSize
	tileNorth := int64(id.Y) * TileSize
	encoded := make([]byte, ScalarTileBytes)
	stats := ScalarStats{DecodedBytes: ScalarTileBytes}
	for textureY := 0; textureY < ScalarTextureSize; textureY++ {
		globalY := tileNorth + int64(textureY-ScalarGutter)
		for textureX := 0; textureX < ScalarTextureSize; textureX++ {
			index := textureY*ScalarTextureSize + textureX
			if globalY < 0 || globalY >= worldPixels {
				encoded[index] = ScalarNoData
				stats.NoDataTexels++
				continue
			}
			globalX := wrapPixel(tileWest+int64(textureX-ScalarGutter), worldPixels)
			longitude, latitude := mercatorTexelCentre(globalX, globalY, worldPixels)
			score, valid, err := sample(ScalarSample{
				GlobalX: globalX, GlobalY: globalY, Longitude: longitude, Latitude: latitude,
			})
			if err != nil {
				return nil, ScalarStats{}, fmt.Errorf("scalar texel %d,%d: %w", textureX, textureY, err)
			}
			if !valid {
				encoded[index] = ScalarNoData
				stats.NoDataTexels++
				continue
			}
			if score > 100 && (!allowFallback || score != ForecastScalarFallback) {
				return nil, ScalarStats{}, fmt.Errorf("scalar texel %d,%d has score %d; want 0..100 or fallback", textureX, textureY, score)
			}
			encoded[index] = score
			stats.DataTexels++
		}
	}
	return encoded, stats, nil
}

// WriteScalar publishes one immutable raw R8 score tile. Static delivery may
// apply ordinary HTTP Content-Encoding compression; after HTTP decoding the
// browser body must still be exactly ScalarTileBytes bytes. All-no-data tiles
// are omitted.
func (writer *Writer) WriteScalar(id ID, period string, sample EvaluatedScoreSampler) (ScalarAsset, error) {
	return writer.writeScalar(id, period, sample, false)
}

// WriteForecastScalar publishes a forecast tile that may contain the reserved
// display-land fallback marker.
func (writer *Writer) WriteForecastScalar(id ID, period string, sample EvaluatedScoreSampler) (ScalarAsset, error) {
	return writer.writeScalar(id, period, sample, true)
}

func (writer *Writer) writeScalar(id ID, period string, sample EvaluatedScoreSampler, allowFallback bool) (ScalarAsset, error) {
	if writer == nil || !validIdentifier(period, periodIDBytes) {
		return ScalarAsset{}, errors.New("tile writer or period is invalid")
	}
	if err := id.validate(); err != nil {
		return ScalarAsset{}, err
	}
	asset := ScalarAsset{
		ID: id, ReleaseID: writer.releaseID, Period: period, MediaType: ScalarMediaType,
	}
	path := filepath.Join(writer.root, "releases", writer.releaseID, "scores", period, id.Tier, fmt.Sprint(id.Zoom), fmt.Sprint(id.X), fmt.Sprintf("%d.nws", id.Y))
	if _, err := os.Stat(path); err == nil {
		return ScalarAsset{}, fmt.Errorf("immutable tile already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return ScalarAsset{}, err
	}

	encoded, stats, err := buildScalarTile(id, sample, allowFallback)
	if err != nil {
		return ScalarAsset{}, err
	}
	asset.DataTexels, asset.NoDataTexels = stats.DataTexels, stats.NoDataTexels
	if stats.DataTexels == 0 {
		asset.Omitted = true
		return asset, nil
	}
	digest := sha256.Sum256(encoded)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ScalarAsset{}, err
	}
	if err := writeImmutable(path, encoded); err != nil {
		return ScalarAsset{}, err
	}
	if err := syncDirectoriesToRoot(writer.root, filepath.Dir(path)); err != nil {
		return ScalarAsset{}, err
	}
	asset.Path = path
	asset.Bytes = uint64(len(encoded))
	asset.SHA256 = hex.EncodeToString(digest[:])
	return asset, nil
}

// ReadScalar validates a raw static tile against its content-addressed digest.
// A manifest consumer must supply the expected digest; without it, an in-range
// byte corruption cannot be distinguished from another valid score.
func ReadScalar(path, expectedSHA256 string) ([]byte, ScalarStats, error) {
	return readScalar(path, expectedSHA256, false)
}

// ReadForecastScalar validates a forecast tile that may contain the reserved
// detail-fallback marker.
func ReadForecastScalar(path, expectedSHA256 string) ([]byte, ScalarStats, error) {
	return readScalar(path, expectedSHA256, true)
}

func readScalar(path, expectedSHA256 string, allowFallback bool) ([]byte, ScalarStats, error) {
	if len(expectedSHA256) != sha256.Size*2 {
		return nil, ScalarStats{}, errors.New("scalar tile requires an expected SHA-256")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, ScalarStats{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, ScalarStats{}, err
	}
	if info.Size() != ScalarTileBytes {
		return nil, ScalarStats{}, fmt.Errorf("scalar tile has %d bytes; want %d", info.Size(), ScalarTileBytes)
	}
	encoded := make([]byte, ScalarTileBytes)
	if _, err := file.ReadAt(encoded, 0); err != nil {
		return nil, ScalarStats{}, err
	}
	digest := sha256.Sum256(encoded)
	if hex.EncodeToString(digest[:]) != expectedSHA256 {
		return nil, ScalarStats{}, errors.New("scalar tile checksum mismatch")
	}
	stats := ScalarStats{DecodedBytes: ScalarTileBytes}
	for index, score := range encoded {
		switch {
		case score <= 100 || (allowFallback && score == ForecastScalarFallback):
			stats.DataTexels++
		case score == ScalarNoData:
			stats.NoDataTexels++
		default:
			return nil, ScalarStats{}, fmt.Errorf("scalar tile texel %d has invalid score %d", index, score)
		}
	}
	return encoded, stats, nil
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
	longitude := x*360 - 180
	latitude := math.Atan(math.Sinh(math.Pi*(1-2*y))) * 180 / math.Pi
	return longitude, latitude
}
