package artifact

import "testing"

func TestValidateManifestRequiresLayers(t *testing.T) {
	err := Validate(Manifest{Version: 1})
	if err == nil {
		t.Fatal("expected missing layers to fail")
	}
}

func TestTileURL(t *testing.T) {
	got := TileURL("forecast", "general", "d1")
	want := "/artifacts/tiles/forecast/general/d1/{z}/{x}/{y}.png"
	if got != want {
		t.Fatalf("tile URL = %s, want %s", got, want)
	}
}
