package dailyforecast

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestForecastColoursChiosWhenItsPeninsulaOwnsTheLandMask(t *testing.T) {
	static := t.TempDir()
	if err := os.Mkdir(filepath.Join(static, "climate"), 0755); err != nil {
		t.Fatal(err)
	}
	const chiosRow, chiosColumn, peninsulaColumn = 207, 825, 826
	offsets := bytes.Repeat([]byte{255}, Width*Height)
	offsets[chiosRow*Width+peninsulaColumn] = 128
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, &image.Gray{Pix: offsets, Stride: Width, Rect: image.Rect(0, 0, Width, Height)}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(static, "climate", "offsets-10.png"), encoded.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	record := []byte{165, 165, 165, 165, 165, 165, 150, 150, 150, 150, 120, 0, 20, 6, 11}
	records := bytes.Repeat(record, v6Days)
	output := t.TempDir()
	cells := [][2]uint16{{51, 206}}
	if err := writeV6ScoreImages(context.Background(), static, output, cells, records, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), io.Discard); err != nil {
		t.Fatal(err)
	}
	levels := v6DefaultDayLevels(record)
	expected := byte(math.Floor(v6CanonicalScore(levels[5])))
	for day := range v6Days + 1 {
		name := fmt.Sprintf("scores-typical-day-day-%02d.png", day)
		if day == v6Days {
			name = "scores-typical-day-all.png"
		}
		file, err := os.Open(filepath.Join(output, name))
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := png.Decode(file)
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		pixels := decoded.(*image.Gray)
		chios := pixels.GrayAt(chiosColumn, chiosRow).Y
		peninsula := pixels.GrayAt(peninsulaColumn, chiosRow).Y
		if chios == 255 || chios != peninsula {
			t.Errorf("%s: Chios=%d, peninsula=%d; both need the same weather colour", name, chios, peninsula)
		}
		if day < v6Days && peninsula != expected {
			t.Errorf("%s: existing peninsula score changed: got %d, want %d", name, peninsula, expected)
		}
		if pixels.GrayAt(chiosColumn, 600).Y == 255 || pixels.GrayAt(chiosColumn, 601).Y != 255 {
			t.Errorf("%s: excluded land must begin south of 60°S", name)
		}
	}
}
