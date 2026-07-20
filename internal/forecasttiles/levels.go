package forecasttiles

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"

	"nice-weather-finder/internal/tiledelivery"
)

type scalarKey struct {
	field Field
	zoom  uint8
	x, y  uint32
}

type levelSampler struct {
	assets    map[scalarKey]tiledelivery.ScalarAsset
	field     Field
	zoom      uint8
	cache     map[scalarKey][]byte
	peakBytes uint64
}

func newLevelSampler(assets map[scalarKey]tiledelivery.ScalarAsset, field Field, zoom uint8) *levelSampler {
	return &levelSampler{assets: assets, field: field, zoom: zoom, cache: make(map[scalarKey][]byte)}
}

func (sampler *levelSampler) parentHasData(x, y uint32) bool {
	for childY := y * 2; childY < y*2+2; childY++ {
		for childX := x * 2; childX < x*2+2; childX++ {
			if _, ok := sampler.assets[scalarKey{field: sampler.field, zoom: sampler.zoom, x: childX, y: childY}]; ok {
				return true
			}
		}
	}
	return false
}

func (sampler *levelSampler) sample(sample tiledelivery.ScalarSample) (uint8, bool, error) {
	childWorld := int64(tiledelivery.TileSize) * (int64(1) << sampler.zoom)
	baseX, baseY := sample.GlobalX*2, sample.GlobalY*2
	total, count := 0, 0
	for offsetY := int64(0); offsetY < 2; offsetY++ {
		childY := baseY + offsetY
		if childY < 0 || childY >= childWorld {
			continue
		}
		for offsetX := int64(0); offsetX < 2; offsetX++ {
			childX := (baseX + offsetX) % childWorld
			if childX < 0 {
				childX += childWorld
			}
			value, valid, err := sampler.read(childX, childY)
			if err != nil {
				return 0, false, err
			}
			if valid {
				total += int(value)
				count++
			}
		}
	}
	if count == 0 {
		return 0, false, nil
	}
	return uint8(math.Round(float64(total) / float64(count))), true, nil
}

func (sampler *levelSampler) read(globalX, globalY int64) (uint8, bool, error) {
	tileX, tileY := uint32(globalX/tiledelivery.TileSize), uint32(globalY/tiledelivery.TileSize)
	key := scalarKey{field: sampler.field, zoom: sampler.zoom, x: tileX, y: tileY}
	asset, ok := sampler.assets[key]
	if !ok {
		return 0, false, nil
	}
	encoded, ok := sampler.cache[key]
	if !ok {
		var err error
		encoded, _, err = tiledelivery.ReadScalar(asset.Path, asset.SHA256)
		if err != nil {
			return 0, false, err
		}
		sampler.cache[key] = encoded
		sampler.peakBytes = max(sampler.peakBytes, uint64(len(sampler.cache))*tiledelivery.ScalarTileBytes)
	}
	column, row := int(globalX%tiledelivery.TileSize)+tiledelivery.ScalarGutter, int(globalY%tiledelivery.TileSize)+tiledelivery.ScalarGutter
	value := encoded[row*tiledelivery.ScalarTextureSize+column]
	if value == tiledelivery.ScalarNoData {
		return 0, false, nil
	}
	return value, true, nil
}

func scalarID(tier string, field Field, zoom uint8, x, y uint32) (tiledelivery.ID, error) {
	suffix := map[Field]string{
		FieldThermal: ".th", FieldDryness: ".dr", FieldSun: ".su", FieldNiceScore: ".ns",
		FieldNiceWind: ".nw", FieldCyclingScore: ".cs", FieldCyclingWind: ".cw",
	}[field]
	if suffix == "" {
		return tiledelivery.ID{}, fmt.Errorf("unknown scalar field %q", field)
	}
	return tiledelivery.NewID(tier+suffix, zoom, int64(x), int64(y))
}

func pointEntry(staging string, asset tiledelivery.PointAsset) (PointTile, error) {
	releaseRoot := filepath.Join(staging, "releases", asset.ReleaseID)
	relative, err := relativeAssetPath(releaseRoot, asset.Path)
	if err != nil {
		return PointTile{}, err
	}
	return PointTile{Zoom: asset.ID.Zoom, X: asset.ID.X, Y: asset.ID.Y, Path: relative, Bytes: asset.Bytes, SHA256: asset.SHA256, PayloadSHA256: asset.PayloadSHA256}, nil
}

func scalarEntry(staging string, asset tiledelivery.ScalarAsset, field Field) (ScalarTile, error) {
	releaseRoot := filepath.Join(staging, "releases", asset.ReleaseID)
	relative, err := relativeAssetPath(releaseRoot, asset.Path)
	if err != nil {
		return ScalarTile{}, err
	}
	return ScalarTile{Field: field, Zoom: asset.ID.Zoom, X: asset.ID.X, Y: asset.ID.Y, Path: relative, Bytes: asset.Bytes, SHA256: asset.SHA256, DataTexels: asset.DataTexels, NoDataTexels: asset.NoDataTexels}, nil
}

func relativeAssetPath(root, path string) (string, error) {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("tile asset %q is outside its staged day", path)
	}
	return filepath.ToSlash(relative), nil
}

func sortCatalog(catalog *Catalog) {
	sort.Slice(catalog.Points, func(i, j int) bool {
		a, b := catalog.Points[i], catalog.Points[j]
		if a.Zoom != b.Zoom {
			return a.Zoom > b.Zoom
		}
		if a.Y != b.Y {
			return a.Y < b.Y
		}
		return a.X < b.X
	})
	sort.Slice(catalog.Scalars, func(i, j int) bool {
		a, b := catalog.Scalars[i], catalog.Scalars[j]
		if a.Field != b.Field {
			return a.Field < b.Field
		}
		if a.Zoom != b.Zoom {
			return a.Zoom > b.Zoom
		}
		if a.Y != b.Y {
			return a.Y < b.Y
		}
		return a.X < b.X
	})
}
