package tiledelivery

import (
	"strings"
	"testing"
)

func TestScalarParentAveragesAvailableChildrenAndPreservesZero(t *testing.T) {
	root := t.TempDir()
	writer, err := NewWriter(root, "test-release")
	if err != nil {
		t.Fatal(err)
	}
	children := make(map[ID]ScalarAsset)
	childID, _ := NewID("probability", 1, 0, 0)
	asset, err := writer.WriteScalar(childID, "month-01", func(sample ScalarSample) (uint8, bool, error) {
		if sample.GlobalX > 3 || sample.GlobalY > 1 {
			return 0, false, nil
		}
		values := [8]uint8{
			10, 20, 0, ScalarNoData,
			ScalarNoData, 40, ScalarNoData, ScalarNoData,
		}
		value := values[sample.GlobalY*4+sample.GlobalX]
		return value, value != ScalarNoData, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	children[childID] = asset
	parentID, _ := NewID("probability", 0, 0, 0)
	parent, err := NewScalarParentSampler(parentID, assetLookup(children))
	if err != nil {
		t.Fatal(err)
	}
	if !parent.HasInteriorData() {
		t.Fatal("parent did not see its direct child data")
	}
	value, valid, err := parent.Sample(ScalarSample{GlobalX: 0, GlobalY: 0})
	if err != nil || !valid || value != 23 {
		t.Fatalf("lower value=%d valid=%v err=%v; want rounded mean 23", value, valid, err)
	}
	value, valid, err = parent.Sample(ScalarSample{GlobalX: 1, GlobalY: 0})
	if err != nil || !valid || value != 0 {
		t.Fatalf("real zero value=%d valid=%v err=%v; want valid zero", value, valid, err)
	}
}

func TestScalarParentBuildWrapsDatelineGutterWithoutWrappingY(t *testing.T) {
	root := t.TempDir()
	writer, err := NewWriter(root, "test-release")
	if err != nil {
		t.Fatal(err)
	}
	children := make(map[ID]ScalarAsset)
	write := func(x, y int64, sample EvaluatedScoreSampler) {
		t.Helper()
		id, idErr := NewID("probability", 1, x, y)
		if idErr != nil {
			t.Fatal(idErr)
		}
		asset, writeErr := writer.WriteScalar(id, "month-01", sample)
		if writeErr != nil {
			t.Fatal(writeErr)
		}
		children[id] = asset
	}
	write(0, 0, func(sample ScalarSample) (uint8, bool, error) {
		if sample.GlobalY <= 1 && sample.GlobalX <= 1 {
			return 10, true, nil
		}
		return 0, false, nil
	})
	write(1, 0, func(sample ScalarSample) (uint8, bool, error) {
		if sample.GlobalY <= 1 && sample.GlobalX >= 510 {
			return 50, true, nil
		}
		return 0, false, nil
	})

	parentID, _ := NewID("probability", 0, 0, 0)
	parent, err := NewScalarParentSampler(parentID, assetLookup(children))
	if err != nil {
		t.Fatal(err)
	}
	encoded, stats, err := BuildScalarTile(parentID, parent.Sample)
	if err != nil {
		t.Fatal(err)
	}
	if got := encoded[ScalarGutter*ScalarTextureSize]; got != 50 {
		t.Fatalf("west dateline gutter=%d; want wrapped east child value 50", got)
	}
	if stats.NoDataTexels < ScalarTextureSize {
		t.Fatalf("north outer gutter no-data=%d; want at least %d", stats.NoDataTexels, ScalarTextureSize)
	}
	for column := 0; column < ScalarTextureSize; column++ {
		if got := encoded[column]; got != ScalarNoData {
			t.Fatalf("north outer gutter column %d=%d; want no-data", column, got)
		}
	}
}

func TestScalarParentCacheIsBoundedToOneParentNeighbourhood(t *testing.T) {
	root := t.TempDir()
	writer, err := NewWriter(root, "test-release")
	if err != nil {
		t.Fatal(err)
	}
	children := make(map[ID]ScalarAsset)
	for y := int64(1); y <= 4; y++ {
		for x := int64(1); x <= 4; x++ {
			id, _ := NewID("probability", 3, x, y)
			asset, writeErr := writer.WriteScalar(id, "month-01", func(ScalarSample) (uint8, bool, error) { return 60, true, nil })
			if writeErr != nil {
				t.Fatal(writeErr)
			}
			children[id] = asset
		}
	}
	parentID, _ := NewID("probability", 2, 1, 1)
	parent, err := NewScalarParentSampler(parentID, assetLookup(children))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := BuildScalarTile(parentID, parent.Sample); err != nil {
		t.Fatal(err)
	}
	if got, want := parent.PeakDecodedBytes(), uint64(16*ScalarTileBytes); got != want {
		t.Fatalf("peak decoded child bytes=%d; want exact one-parent neighbourhood %d", got, want)
	}
}

func TestScalarParentRejectsWrongChildIdentity(t *testing.T) {
	parentID, _ := NewID("probability", 0, 0, 0)
	wrongID, _ := NewID("other", 1, 0, 0)
	parent, err := NewScalarParentSampler(parentID, func(ID) (ScalarAsset, bool) {
		return ScalarAsset{ID: wrongID}, true
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = parent.Sample(ScalarSample{GlobalX: 0, GlobalY: 0})
	if err == nil || !strings.Contains(err.Error(), "invalid asset") {
		t.Fatalf("wrong child identity error=%v", err)
	}
}

func assetLookup(assets map[ID]ScalarAsset) ScalarAssetLookup {
	return func(id ID) (ScalarAsset, bool) {
		asset, ok := assets[id]
		return asset, ok
	}
}
