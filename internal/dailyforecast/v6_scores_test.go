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
		reasonName := fmt.Sprintf("reasons-typical-day-day-%02d.png", day)
		if day == v6Days {
			reasonName = "reasons-typical-day-all.png"
		}
		reasonFile, err := os.Open(filepath.Join(output, reasonName))
		if err != nil {
			t.Fatal(err)
		}
		decodedReason, err := png.Decode(reasonFile)
		reasonFile.Close()
		if err != nil {
			t.Fatal(err)
		}
		reasonPixels := decodedReason.(*image.Gray)
		for _, column := range []int{chiosColumn, peninsulaColumn} {
			if actual := reasonPixels.GrayAt(column, chiosRow).Y; actual != 20 {
				t.Errorf("%s: expected grey with no below-Nice severity (20), got %d", reasonName, actual)
			}
		}
		for index, value := range pixels.Pix {
			if (value == 255) != (reasonPixels.Pix[index] == 255) {
				t.Fatalf("%s: reason mask differs from score at pixel %d", reasonName, index)
			}
		}
	}
}

func TestForecastDayReasonsNameWhatSpoiledTheDay(t *testing.T) {
	perfect := []byte{165, 165, 165, 165, 165, 165, 150, 150, 150, 150, 120, 0, 20, 11, 11}
	for expected, name := range []string{"cold", "hot", "wet", "windy", "muggy", "grey"} {
		t.Run(name, func(t *testing.T) {
			record := bytes.Clone(perfect)
			switch name {
			case "cold":
				for index := range 6 {
					record[index] = 117
				}
			case "hot":
				for index := 6; index < 10; index++ {
					record[index] = 207
				}
			case "wet":
				record[11] = 150
			case "windy":
				record[12] = 120
			case "muggy":
				record[10] = 171
			case "grey":
				record[13] = 0
			}
			if actual := v6DefaultDayReasonLevels(record)[5]; actual != byte(expected) {
				t.Fatalf("reason = %d, want %d (%s)", actual, expected, name)
			}
		})
	}
	if actual := v6DefaultDayReasonLevels(perfect)[5]; actual != 0 {
		t.Fatalf("perfect-day tie should retain cold, got %d", actual)
	}
}

func TestForecastReasonSeverityUsesThePublishedScoreBands(t *testing.T) {
	for _, scenario := range []struct {
		score    float64
		severity byte
	}{
		{100, 0}, {55, 0}, {54.9999, 1}, {40, 1}, {39.9999, 2}, {15, 2}, {14.9999, 3}, {0, 3},
	} {
		if actual := v6ReasonImageByte(5, scenario.score); actual != 20+scenario.severity {
			t.Errorf("score %g: byte=%d, want %d", scenario.score, actual, 20+scenario.severity)
		}
	}
}
