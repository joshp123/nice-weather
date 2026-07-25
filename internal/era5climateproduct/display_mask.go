package era5climateproduct

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"nice-weather-finder/internal/providers/era5hourly"
)

const DisplayMaskAuthority = "Natural Earth 5.1.1 centre-on-land at exact ERA5 0.25 degree grid points"

// DisplayMask is an opaque, checksum-bound centre-on-land plane. It controls
// map visibility only; source-plan coastal support remains available to point
// lookup.
type DisplayMask struct {
	values           []byte
	sha256           string
	landSourceSHA256 string
	derivation       string
	centreLandCells  int
}

// LoadDisplayMask accepts the one canonical 721x1440 centre-on-land plane
// whose digest and Natural Earth source are bound into the trusted cell plan.
func LoadDisplayMask(path string, plan era5hourly.CellPlan) (DisplayMask, error) {
	landSourceSHA256, derivation, expectedSHA256, centreLandCells, ok := plan.DisplayMaskAuthority()
	if path == "" || !ok || !validSHA256(landSourceSHA256) || !validSHA256(expectedSHA256) ||
		centreLandCells < 1 || centreLandCells >= Rows*Columns {
		return DisplayMask{}, errors.New("ERA5 display mask requires canonical authority from its trusted cell plan")
	}
	file, err := os.Open(path)
	if err != nil {
		return DisplayMask{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return DisplayMask{}, err
	}
	wantBytes := int64(Rows * Columns)
	if !info.Mode().IsRegular() || info.Size() != wantBytes {
		return DisplayMask{}, fmt.Errorf("ERA5 display mask has %d bytes; want %d", info.Size(), wantBytes)
	}
	values := make([]byte, wantBytes)
	if _, err := io.ReadFull(file, values); err != nil {
		return DisplayMask{}, err
	}
	var trailing [1]byte
	if count, err := file.Read(trailing[:]); err != io.EOF || count != 0 {
		return DisplayMask{}, errors.New("ERA5 display mask changed while it was read")
	}
	digest := sha256.Sum256(values)
	actual := hex.EncodeToString(digest[:])
	if actual != expectedSHA256 {
		return DisplayMask{}, errors.New("ERA5 display mask checksum mismatch")
	}
	landCells := 0
	for index, value := range values {
		if value > 1 {
			return DisplayMask{}, fmt.Errorf("ERA5 display mask cell %d has value %d; want 0 or 1", index, value)
		}
		landCells += int(value)
	}
	if landCells != centreLandCells {
		return DisplayMask{}, fmt.Errorf("ERA5 display mask has %d land centres; trusted plan requires %d", landCells, centreLandCells)
	}
	return DisplayMask{
		values: values, sha256: actual, landSourceSHA256: landSourceSHA256,
		derivation: derivation, centreLandCells: centreLandCells,
	}, nil
}

func (mask DisplayMask) matchesPlan(plan era5hourly.CellPlan) bool {
	landSourceSHA256, derivation, maskSHA256, centreLandCells, ok := plan.DisplayMaskAuthority()
	return ok && len(mask.values) == Rows*Columns && validSHA256(mask.sha256) &&
		mask.sha256 == maskSHA256 && mask.landSourceSHA256 == landSourceSHA256 &&
		mask.derivation == derivation && mask.centreLandCells == centreLandCells
}
