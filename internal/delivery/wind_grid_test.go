package delivery

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestWindGridRoundTripAndQuantization(t *testing.T) {
	path := filepath.Join(t.TempDir(), "frame.nww")
	spec := GridSpec{Bounds: GridBounds{West: -1, East: 1, South: 50, North: 51}, Rows: 1, Cols: 2}
	written, clipped, err := WriteWindGrid(path, spec, []WindVector{{U: 3.24, V: -4.26, Valid: true}, {}})
	if err != nil {
		t.Fatal(err)
	}
	if written.Version != WindGridVersion || written.RecordSize != windGridRecordSize || len(written.SHA256) != 64 {
		t.Fatalf("unexpected metadata: %+v", written)
	}
	if clipped != 0 {
		t.Fatalf("clipped components = %d", clipped)
	}
	reader, err := OpenWindGrid(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	first, err := reader.Vector(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if first != (WindVector{U: 3, V: -4.5, Valid: true}) {
		t.Fatalf("quantized vector = %+v", first)
	}
	second, err := reader.Vector(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if second != (WindVector{}) {
		t.Fatalf("invalid vector = %+v", second)
	}
}

func TestWindGridRejectsHiddenInvalidAndSaturatesExtremeComponents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "frame.nww")
	spec := GridSpec{Bounds: GridBounds{West: 0, East: 1, South: 0, North: 1}, Rows: 1, Cols: 1}
	for name, vector := range map[string]WindVector{
		"hidden invalid": {U: 1},
		"not finite":     {U: float32(math.NaN()), V: 0, Valid: true},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := WriteWindGrid(path, spec, []WindVector{vector}); err == nil {
				t.Fatal("lossy wind vector was accepted")
			}
		})
	}
	metadata, clipped, err := WriteWindGrid(path, spec, []WindVector{{U: 80, V: -80, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if clipped != 2 || metadata.SHA256 == "" {
		t.Fatalf("saturation metadata = (%+v, %d)", metadata, clipped)
	}
	reader, err := OpenWindGrid(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	vector, err := reader.Vector(0, 0)
	if err != nil || vector.U != 63.5 || vector.V != -63.5 {
		t.Fatalf("saturated vector = (%+v, %v)", vector, err)
	}
}

func TestWindGridRejectsCorruptionAndPartialNoData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "frame.nww")
	spec := GridSpec{Bounds: GridBounds{West: 0, East: 1, South: 0, North: 1}, Rows: 1, Cols: 1}
	if _, _, err := WriteWindGrid(path, spec, []WindVector{{U: 2, V: 3, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"checksum": mutate(original, gridHeaderSize),
		"reserved": mutate(original, 100),
		"extra":    append(append([]byte(nil), original...), 0),
		"partial no data": func() []byte {
			changed := append([]byte(nil), original...)
			changed[gridHeaderSize] = windNoDataByte
			return changed
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			candidate := filepath.Join(dir, name+".nww")
			if err := os.WriteFile(candidate, data, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := ValidateWindGrid(candidate); err == nil {
				t.Fatal("invalid wind grid was accepted")
			}
		})
	}
}

func TestWindGridGzipCreatesNestedStaticArtifactAndValidatesBothChecksums(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release", "wind", "frame.nww.gz")
	spec := GridSpec{Bounds: GridBounds{West: -1, East: 1, South: 50, North: 51}, Rows: 1, Cols: 1}
	written, err := WriteWindGridGzip(path, spec, []WindVector{{U: 3.24, V: -4.26, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if written.Bytes <= 0 || len(written.SHA256) != 64 || written.Grid.SHA256 == written.SHA256 {
		t.Fatalf("compressed metadata = %+v", written)
	}
	validated, err := ValidateWindGridGzip(path)
	if err != nil {
		t.Fatal(err)
	}
	if validated.SHA256 != written.SHA256 || validated.Grid.SHA256 != written.Grid.SHA256 {
		t.Fatalf("validated metadata = %+v; written %+v", validated, written)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(append([]byte(nil), data...), data...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateWindGridGzip(path); err == nil {
		t.Fatal("concatenated compressed wind artifact was accepted")
	}
	data[len(data)-1] ^= 0xff
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateWindGridGzip(path); err == nil {
		t.Fatal("corrupted compressed wind artifact was accepted")
	}
}
