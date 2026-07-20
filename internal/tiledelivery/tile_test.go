package tiledelivery

import (
	"math"
	"os"
	"strings"
	"sync"
	"testing"

	"nice-weather-finder/internal/delivery"
)

func TestXYZBoundsAndCanonicalAddresses(t *testing.T) {
	for _, raw := range []struct {
		zoom uint8
		x, y int64
	}{{0, 0, 0}, {1, 1, 1}, {5, 16, 10}, {14, 8396, 5470}} {
		id, err := NewID("global", raw.zoom, raw.x, raw.y)
		if err != nil {
			t.Fatal(err)
		}
		bounds, err := id.Bounds()
		if err != nil {
			t.Fatal(err)
		}
		count := math.Exp2(float64(id.Zoom))
		if !near((bounds.West+180)/360, float64(id.X)/count) || !near((bounds.East+180)/360, float64(id.X+1)/count) {
			t.Fatalf("%+v bounds = %+v", id, bounds)
		}
	}
	if _, err := NewID("global", 3, 0, 8); err == nil {
		t.Fatal("accepted Y outside zoom")
	}
}

func TestTileEdgeOwnership(t *testing.T) {
	id, _ := NewID("global", 3, 4, 3)
	bounds, _ := id.Bounds()
	got, row, col, err := At("global", id.Zoom, bounds.West, bounds.North)
	if err != nil || got != id || row != 0 || col != 0 {
		t.Fatalf("north-west edge = %+v %d,%d %v; want %+v 0,0", got, row, col, err, id)
	}
	east, _, _, err := At("global", id.Zoom, bounds.East, (bounds.North+bounds.South)/2)
	if err != nil || east.X != id.X+1 {
		t.Fatalf("east edge = %+v, %v", east, err)
	}
	south, _, _, err := At("global", id.Zoom, (bounds.West+bounds.East)/2, bounds.South)
	if err != nil || south.Y != id.Y+1 {
		t.Fatalf("south edge = %+v, %v", south, err)
	}
}

func TestEveryMercatorEdgeUsesSouthEastOwner(t *testing.T) {
	for zoom := uint8(0); zoom <= 8; zoom++ {
		count := uint32(1) << zoom
		for x := uint32(0); x < count; x++ {
			for y := uint32(0); y < count; y++ {
				id := ID{Tier: "global", Zoom: zoom, X: x, Y: y}
				bounds, err := id.Bounds()
				if err != nil {
					t.Fatal(err)
				}
				got, row, col, err := At(id.Tier, zoom, bounds.West, bounds.North)
				if err != nil || got != id || row != 0 || col != 0 {
					t.Fatalf("north-west edge of %+v = %+v %d,%d, %v", id, got, row, col, err)
				}
				eastID, _, _, err := At(id.Tier, zoom, bounds.East, (bounds.North+bounds.South)/2)
				wantEast := id
				wantEast.X = (x + 1) % count
				if err != nil || eastID != wantEast {
					t.Fatalf("east edge of %+v = %+v, %v; want %+v", id, eastID, err, wantEast)
				}
				southID, southRow, _, err := At(id.Tier, zoom, (bounds.West+bounds.East)/2, bounds.South)
				wantSouth := id
				wantRow := 0
				if y+1 < count {
					wantSouth.Y++
				} else {
					wantRow = TileSize - 1
				}
				if err != nil || southID != wantSouth || southRow != wantRow {
					t.Fatalf("south edge of %+v = %+v row %d, %v; want %+v row %d", id, southID, southRow, err, wantSouth, wantRow)
				}
			}
		}
	}
}

func TestAntimeridianWrapAndMercatorLatitudePolicy(t *testing.T) {
	east, er, ec, err := At("global", 4, 180, 0)
	if err != nil {
		t.Fatal(err)
	}
	west, wr, wc, err := At("global", 4, -180, 0)
	if err != nil {
		t.Fatal(err)
	}
	if east != west || er != wr || ec != wc || east.X != 0 || ec != 0 {
		t.Fatalf("antimeridian differs: east=%+v/%d,%d west=%+v/%d,%d", east, er, ec, west, wr, wc)
	}
	wrapped, _, _, err := At("global", 4, 540, 0)
	if err != nil || wrapped != west {
		t.Fatalf("wrapped longitude = %+v, %v", wrapped, err)
	}
	for _, latitude := range []float64{85.0511287798066, -85.0511287798066} {
		if _, _, _, err := At("global", 4, 0, latitude); err != nil {
			t.Fatalf("rejected Mercator boundary %g: %v", latitude, err)
		}
	}
	for _, latitude := range []float64{85.0511287798067, -85.0511287798067} {
		if _, _, _, err := At("global", 4, 0, latitude); err == nil {
			t.Fatalf("accepted latitude %g", latitude)
		}
	}
}

func TestEuropePointTileUsesExactMercatorRecord(t *testing.T) {
	const longitude, latitude = -4.4214, 36.7213
	id, row, col, err := At("europe-detail", 7, longitude, latitude)
	if err != nil {
		t.Fatal(err)
	}
	records := make([]delivery.DisplayScoreRecord, TileSize*TileSize)
	want := displayRecord(86)
	records[row*TileSize+col] = want
	writer, err := NewWriter(t.TempDir(), "release-canary")
	if err != nil {
		t.Fatal(err)
	}
	asset, err := writer.Write(id, "day-01", records)
	if err != nil {
		t.Fatal(err)
	}
	if asset.Omitted || asset.Bytes != pointHeaderSize+TileSize*TileSize*delivery.DisplayScoreRecordBytes {
		t.Fatalf("asset = %+v", asset)
	}
	t.Logf("direct 256x256 display-score tile: %d bytes", asset.Bytes)
	for _, fragment := range []string{"release-canary", "day-01", "europe-detail"} {
		if !strings.Contains(asset.Path, fragment) {
			t.Fatalf("path %q lacks %q", asset.Path, fragment)
		}
	}
	reader, err := OpenPoint(asset.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	got, ok, err := reader.At(longitude, latitude)
	if err != nil || !ok || got != want {
		t.Fatalf("point = %+v, %t, %v; want %+v", got, ok, err, want)
	}
	// Adjacent water/no-data remains absent; it cannot inherit the land record.
	waterLongitude := longitude + (asset.Bounds.East-asset.Bounds.West)/TileSize
	if _, ok, err := reader.At(waterLongitude, latitude); err != nil || ok {
		t.Fatalf("water point = %t, %v", ok, err)
	}
	meta := reader.Metadata()
	if meta.ID != id || meta.ReleaseID != "release-canary" || meta.Period != "day-01" || meta.SHA256 != asset.SHA256 {
		t.Fatalf("metadata = %+v", meta)
	}
}

func TestPointTileRejectsPayloadCorruption(t *testing.T) {
	root := t.TempDir()
	writer, _ := NewWriter(root, "release-canary")
	id, _ := NewID("global", 2, 1, 1)
	records := make([]delivery.DisplayScoreRecord, TileSize*TileSize)
	records[0] = displayRecord(50)
	asset, err := writer.Write(id, "day-01", records)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(asset.Path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte{99}, pointHeaderSize); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPoint(asset.Path); err == nil {
		t.Fatal("accepted corrupted point payload")
	}
}

func TestAllWaterTileIsOmitted(t *testing.T) {
	root := t.TempDir()
	writer, _ := NewWriter(root, "release-canary")
	id, _ := NewID("global", 2, 1, 1)
	asset, err := writer.Write(id, "day-01", make([]delivery.DisplayScoreRecord, TileSize*TileSize))
	if err != nil {
		t.Fatal(err)
	}
	if !asset.Omitted || asset.Path != "" {
		t.Fatalf("all-water asset = %+v", asset)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("all-water tile wrote %d entries", len(entries))
	}
}

func TestIdentityPreflightWritesNothing(t *testing.T) {
	root := t.TempDir()
	writer, _ := NewWriter(root, "release-canary")
	id, _ := NewID("global", 2, 1, 1)
	records := make([]delivery.DisplayScoreRecord, TileSize*TileSize)
	records[0] = displayRecord(50)
	if _, err := writer.Write(id, "BAD PERIOD", records); err == nil {
		t.Fatal("accepted invalid period")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("invalid identity wrote files")
	}
}

func TestProductionIdentitiesAreAccepted(t *testing.T) {
	writer, err := NewWriter(t.TempDir(), "live-2026071800-v9-20260718T231653Z")
	if err != nil {
		t.Fatal(err)
	}
	id, err := NewID("europe-detail", 2, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	records := make([]delivery.DisplayScoreRecord, TileSize*TileSize)
	records[0] = displayRecord(50)
	if _, err := writer.Write(id, "2026-07-20", records); err != nil {
		t.Fatal(err)
	}
}

func TestPointTileIsImmutableForDataAndOmission(t *testing.T) {
	writer, err := NewWriter(t.TempDir(), "release-canary")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := NewID("global", 2, 1, 1)
	records := make([]delivery.DisplayScoreRecord, TileSize*TileSize)
	records[0] = displayRecord(50)
	asset, err := writer.Write(id, "day-01", records)
	if err != nil {
		t.Fatal(err)
	}

	replacement := make([]delivery.DisplayScoreRecord, TileSize*TileSize)
	replacement[0] = displayRecord(90)
	if _, err := writer.Write(id, "day-01", replacement); err == nil {
		t.Fatal("overwrote an immutable point tile")
	}
	if _, err := writer.Write(id, "day-01", make([]delivery.DisplayScoreRecord, TileSize*TileSize)); err == nil {
		t.Fatal("reported an existing point tile as omitted")
	}

	reader, err := OpenPoint(asset.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	bounds, _ := id.Bounds()
	record, ok, err := reader.At(bounds.West, bounds.North)
	if err != nil || !ok || record.NiceScore != 50 {
		t.Fatalf("immutable record = %+v, %t, %v", record, ok, err)
	}
}

func TestConcurrentPointTilePublicationHasOneWinner(t *testing.T) {
	writer, err := NewWriter(t.TempDir(), "release-canary")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := NewID("global", 2, 1, 1)
	const writers = 8
	results := make(chan error, writers)
	var wait sync.WaitGroup
	for index := 0; index < writers; index++ {
		wait.Add(1)
		go func(score uint8) {
			defer wait.Done()
			records := make([]delivery.DisplayScoreRecord, TileSize*TileSize)
			records[0] = displayRecord(score)
			_, err := writer.Write(id, "day-01", records)
			results <- err
		}(uint8(50 + index))
	}
	wait.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent immutable writes had %d winners; want 1", successes)
	}
}

func displayRecord(score uint8) delivery.DisplayScoreRecord {
	return delivery.DisplayScoreRecord{Thermal: score, Dryness: score, Sun: score, NiceScore: score, NiceWind: score, CyclingScore: score, CyclingWind: score, Valid: true}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-14 }
