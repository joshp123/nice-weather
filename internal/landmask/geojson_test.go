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
