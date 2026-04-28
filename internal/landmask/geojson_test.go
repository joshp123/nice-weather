package landmask

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGeoJSONMaskContainsLandAndRejectsSea(t *testing.T) {
	path := filepath.Join(t.TempDir(), "land.geojson")
	data := `{
  "type": "FeatureCollection",
  "features": [
    {"type": "Feature", "geometry": {"type": "Polygon", "coordinates": [[[0,0],[10,0],[10,10],[0,10],[0,0]]]}}
  ]
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	mask, err := LoadGeoJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if !mask.Contains(5, 5) {
		t.Fatal("expected point inside fixture land")
	}
	if mask.Contains(20, 20) {
		t.Fatal("expected point outside fixture land")
	}
}

func TestGeoJSONMaskRespectsHoles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "land.geojson")
	data := `{
  "type": "FeatureCollection",
  "features": [
    {"type": "Feature", "geometry": {"type": "Polygon", "coordinates": [
      [[0,0],[10,0],[10,10],[0,10],[0,0]],
      [[4,4],[6,4],[6,6],[4,6],[4,4]]
    ]}}
  ]
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	mask, err := LoadGeoJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if mask.Contains(5, 5) {
		t.Fatal("expected point inside polygon hole to be sea")
	}
	if !mask.Contains(2, 2) {
		t.Fatal("expected point inside outer polygon to be land")
	}
}

func TestGeoJSONMaskTouchesNearbyCellFootprint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "land.geojson")
	data := `{
  "type": "FeatureCollection",
  "features": [
    {"type": "Feature", "geometry": {"type": "Polygon", "coordinates": [[[1.45,1.45],[1.55,1.45],[1.55,1.55],[1.45,1.55],[1.45,1.45]]]}}
  ]
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	mask, err := LoadGeoJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if mask.Contains(1, 1) {
		t.Fatal("fixture should not contain the cell center")
	}
	if !mask.TouchesCell(1, 1, 0.5) {
		t.Fatal("expected nearby land to keep the grid cell visible")
	}
	if mask.TouchesCell(0, 0, 0.5) {
		t.Fatal("expected distant sea cell to stay hidden")
	}
}

func TestGeoJSONMaskKeepsTinyIslandInsideCell(t *testing.T) {
	path := filepath.Join(t.TempDir(), "land.geojson")
	data := `{
  "type": "FeatureCollection",
  "features": [
    {"type": "Feature", "geometry": {"type": "Polygon", "coordinates": [[[1.18,1.18],[1.22,1.18],[1.22,1.22],[1.18,1.22],[1.18,1.18]]]}}
  ]
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	mask, err := LoadGeoJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if !mask.TouchesCell(1, 1, 0.5) {
		t.Fatal("expected tiny island inside cell to keep the cell visible")
	}
}

func TestGeoJSONMaskKeepsThinLandCrossingCell(t *testing.T) {
	path := filepath.Join(t.TempDir(), "land.geojson")
	data := `{
  "type": "FeatureCollection",
  "features": [
    {"type": "Feature", "geometry": {"type": "Polygon", "coordinates": [[[0.2,1.05],[1.8,1.05],[1.8,1.08],[0.2,1.08],[0.2,1.05]]]}}
  ]
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	mask, err := LoadGeoJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if !mask.TouchesCell(1, 1, 0.1) {
		t.Fatal("expected thin land crossing cell to keep the cell visible")
	}
}
