package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/forecasttiles"
	"nice-weather-finder/internal/tiledelivery"
)

// This opt-in audit reads an already-built real canary. It performs no
// download and never touches a current release pointer.
func TestLiveGlobalCanaryArtifact(t *testing.T) {
	root := os.Getenv("NICE_WEATHER_GLOBAL_CANARY")
	if root == "" {
		t.Skip("set NICE_WEATHER_GLOBAL_CANARY to an isolated completed canary root")
	}
	catalogs, err := filepath.Glob(filepath.Join(root, "canary-runs", "*", "forecast-days", "*", "global", "forecast-tiles.json"))
	if err != nil || len(catalogs) != productDays {
		t.Fatalf("find %d global canary catalogs: %v, %v", productDays, catalogs, err)
	}
	var releaseID string
	var priorDate time.Time
	for index, catalogPath := range catalogs {
		catalog := auditLiveGlobalCanaryDay(t, catalogPath)
		date, err := time.Parse("2006-01-02", catalog.Date)
		if err != nil {
			t.Fatal(err)
		}
		if index > 0 && !date.Equal(priorDate.AddDate(0, 0, 1)) {
			t.Fatalf("live canary dates are not consecutive: %s after %s", date, priorDate)
		}
		if releaseID == "" {
			releaseID = catalog.ReleaseID
		} else if catalog.ReleaseID != releaseID {
			t.Fatalf("live canary mixed release IDs %q and %q", releaseID, catalog.ReleaseID)
		}
		priorDate = date
	}
	if _, err := os.Stat(filepath.Join(root, "current")); !os.IsNotExist(err) {
		t.Fatalf("isolated canary unexpectedly has a current publication pointer: %v", err)
	}
}

func auditLiveGlobalCanaryDay(t *testing.T, catalogPath string) forecasttiles.Catalog {
	t.Helper()
	data, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	var catalog forecasttiles.Catalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	if catalog.Date != catalog.Period || filepath.Base(filepath.Dir(filepath.Dir(catalogPath))) != catalog.Date || catalog.Tier != "global" || catalog.MaxZoom != 3 {
		t.Fatalf("unexpected live canary identity: %+v", catalog)
	}
	base := filepath.Dir(catalogPath)
	for _, point := range catalog.Points {
		path := filepath.Join(base, filepath.FromSlash(point.Path))
		assertFileSHA256(t, path, point.SHA256)
		reader, err := tiledelivery.OpenPoint(path)
		if err != nil {
			t.Fatalf("validate point %s: %v", point.Path, err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for _, scalar := range catalog.Scalars {
		path := filepath.Join(base, filepath.FromSlash(scalar.Path))
		assertFileSHA256(t, path, scalar.SHA256)
		payload, stats, err := tiledelivery.ReadScalar(path, scalar.SHA256)
		if err != nil {
			t.Fatalf("validate scalar %s: %v", scalar.Path, err)
		}
		if len(payload) != tiledelivery.ScalarTileBytes || stats.DataTexels != scalar.DataTexels || stats.NoDataTexels != scalar.NoDataTexels {
			t.Fatalf("scalar catalog mismatch for %s", scalar.Path)
		}
	}

	places := []struct {
		name     string
		lon, lat float64
	}{
		{"Amsterdam", 4.9041, 52.3676},
		{"Madrid", -3.7038, 40.4168},
		{"Singapore", 103.8198, 1.3521},
		{"Canberra", 149.1300, -35.2809},
		{"Sao Paulo", -46.6333, -23.5505},
	}
	for _, place := range places {
		record, scalar, ok := liveCanaryPointAndScalar(t, base, catalog, place.lon, place.lat)
		if !ok {
			t.Fatalf("%s has no evaluated land record", place.name)
		}
		if scalar != record.NiceScore {
			t.Fatalf("%s point nice score %d != scalar %d", place.name, record.NiceScore, scalar)
		}
		t.Logf("%s nice=%d thermal=%d dry=%d sun=%d wind=%d", place.name, record.NiceScore, record.Thermal, record.Dryness, record.Sun, record.NiceWind)
	}
	if _, _, ok := liveCanaryPointAndScalar(t, base, catalog, -140, 0); ok {
		t.Fatal("central Pacific ocean point contains a weather judgement")
	}

	missing := 0
	for y := int64(0); y < 8; y++ {
		for x := int64(0); x < 8; x++ {
			id, _ := tiledelivery.NewID("global", 3, x, y)
			found := false
			for _, point := range catalog.Points {
				if point.Zoom == id.Zoom && point.X == id.X && point.Y == id.Y {
					found = true
					break
				}
			}
			if !found {
				missing++
			}
		}
	}
	if missing != 4 {
		t.Fatalf("omitted max-zoom ocean tiles = %d, want 4", missing)
	}
	return catalog
}

func liveCanaryPointAndScalar(t *testing.T, base string, catalog forecasttiles.Catalog, longitude, latitude float64) (delivery.DisplayScoreRecord, uint8, bool) {
	t.Helper()
	id, row, col, err := tiledelivery.At("global", 3, longitude, latitude)
	if err != nil {
		t.Fatal(err)
	}
	var pointPath, scalarPath, scalarSHA string
	for _, point := range catalog.Points {
		if point.Zoom == id.Zoom && point.X == id.X && point.Y == id.Y {
			pointPath = point.Path
			break
		}
	}
	if pointPath == "" {
		return delivery.DisplayScoreRecord{}, 0, false
	}
	for _, asset := range catalog.Scalars {
		if asset.Field == forecasttiles.FieldNiceScore && asset.Zoom == id.Zoom && asset.X == id.X && asset.Y == id.Y {
			scalarPath, scalarSHA = asset.Path, asset.SHA256
			break
		}
	}
	reader, err := tiledelivery.OpenPoint(filepath.Join(base, filepath.FromSlash(pointPath)))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	decoded, valid, err := reader.At(longitude, latitude)
	if err != nil {
		t.Fatal(err)
	}
	if !valid {
		return delivery.DisplayScoreRecord{}, 0, false
	}
	payload, _, err := tiledelivery.ReadScalar(filepath.Join(base, filepath.FromSlash(scalarPath)), scalarSHA)
	if err != nil {
		t.Fatal(err)
	}
	return decoded, payload[(row+1)*tiledelivery.ScalarTextureSize+col+1], true
}

func assertFileSHA256(t *testing.T, path, want string) {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	if got := hex.EncodeToString(digest[:]); got != want {
		t.Fatalf("SHA-256 %s = %s, want %s", path, got, want)
	}
}
