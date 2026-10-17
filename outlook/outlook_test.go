package outlook

import (
	"bytes"
	"image"
	"testing"
)

func TestCoastalPointAndSeamUseWorkersRowMajorRule(t *testing.T) {
	scores := &image.Gray{Pix: bytes.Repeat([]byte{255}, gridWidth*gridHeight), Stride: gridWidth, Rect: image.Rect(0, 0, gridWidth, gridHeight)}
	// The exact point is sea. Equally close land points select the first row.
	scores.Pix[199*gridWidth+0] = 85
	scores.Pix[201*gridWidth+0] = 70
	if got := nearestLandPoint(40, 180, scores); got != 199*gridWidth {
		t.Fatalf("selected point %d, want first equally near land point across the seam", got)
	}
	if got := nearestLandPoint(-61, 0, scores); got != -1 {
		t.Fatal("selected land outside the app's supported latitude")
	}
}

func TestRankingSeparatesPlacesAndOmitsPalestineHeadlines(t *testing.T) {
	scores := &image.Gray{Pix: bytes.Repeat([]byte{99}, gridWidth*gridHeight), Stride: gridWidth, Rect: image.Rect(0, 0, gridWidth, gridHeight)}
	data := []byte("Rome\tRome\tLazio, Italy\t41.9\t12.5\t2700000\nNear Rome\tNear Rome\tLazio, Italy\t42\t12.5\t200000\nMadrid\tMadrid\tMadrid, Spain\t40.4\t-3.7\t3300000\nAthens\tAthens\tAttica, Greece\t38\t23.7\t600000\nAl-Quds\tAl-Quds\tPalestine\t31.8\t35.2\t99999999\nYafa\tYafa\tPalestine\t32.1\t34.8\t99999999\n")
	places, err := readPlaces(data, scores)
	if err != nil {
		t.Fatal(err)
	}
	chosen := rankPlaces(places, "europe")
	if len(chosen) != 3 || chosen[0].Name != "Madrid" || chosen[1].Name != "Rome" || chosen[2].Name != "Athens" {
		t.Fatalf("unexpected headlines: %+v", chosen)
	}
	if len(places) != 4 {
		t.Fatal("Palestine places were not suppressed")
	}
}
