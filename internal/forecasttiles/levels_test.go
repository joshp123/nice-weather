package forecasttiles

import (
	"testing"

	"nice-weather-finder/internal/tiledelivery"
)

func TestLowerZoomScalarAveragesCanonicalChildrenAndWrapsDatelineGutter(t *testing.T) {
	root := t.TempDir()
	writer, err := tiledelivery.NewWriter(root, "test-release")
	if err != nil {
		t.Fatal(err)
	}
	assets := make(map[scalarKey]tiledelivery.ScalarAsset)
	write := func(x uint32, sample tiledelivery.EvaluatedScoreSampler) {
		t.Helper()
		id, err := tiledelivery.NewID("ifs.ns", 1, int64(x), 0)
		if err != nil {
			t.Fatal(err)
		}
		asset, err := writer.WriteScalar(id, "2026-07-20", sample)
		if err != nil {
			t.Fatal(err)
		}
		assets[scalarKey{field: FieldNiceScore, zoom: 1, x: x, y: 0}] = asset
	}
	write(0, func(sample tiledelivery.ScalarSample) (uint8, bool, error) {
		if sample.GlobalY > 1 || sample.GlobalX > 1 {
			return 0, false, nil
		}
		// One missing child proves lower levels average available evaluated
		// evidence instead of treating no-data as score zero.
		values := [4]uint8{10, 20, tiledelivery.ScalarNoData, 40}
		value := values[sample.GlobalY*2+sample.GlobalX]
		return value, value != tiledelivery.ScalarNoData, nil
	})
	write(1, func(sample tiledelivery.ScalarSample) (uint8, bool, error) {
		if sample.GlobalY <= 1 && sample.GlobalX >= 510 {
			return 50, true, nil
		}
		return 0, false, nil
	})

	sampler := newLevelSampler(assets, FieldNiceScore, 1)
	value, valid, err := sampler.sample(tiledelivery.ScalarSample{GlobalX: 0, GlobalY: 0})
	if err != nil || !valid || value != 23 {
		t.Fatalf("lower value=%d valid=%v err=%v; want rounded mean 23", value, valid, err)
	}
	parent, err := tiledelivery.NewID("ifs.ns", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _, err := tiledelivery.BuildScalarTile(parent, sampler.sample)
	if err != nil {
		t.Fatal(err)
	}
	if got := encoded[tiledelivery.ScalarGutter*tiledelivery.ScalarTextureSize+tiledelivery.ScalarGutter]; got != 23 {
		t.Fatalf("parent interior=%d; want 23", got)
	}
	if got := encoded[tiledelivery.ScalarGutter*tiledelivery.ScalarTextureSize]; got != 50 {
		t.Fatalf("west dateline gutter=%d; want wrapped east child value 50", got)
	}
}
