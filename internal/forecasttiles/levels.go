package forecasttiles

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"nice-weather-finder/internal/tiledelivery"
)

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
