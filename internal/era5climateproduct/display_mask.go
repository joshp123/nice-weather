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
	values                     []byte
	sha256                     string
	landSourceSHA256           string
	derivation                 string
	centreLandCells            int
	acquisitionPlanSHA256      string
	logicalPlanSHA256          string
	publicationAuthorityKind   string
	publicationAuthoritySHA256 string
}

// LoadDisplayMaskWithAuthority validates the exact centre-on-land mask against
// its checksum-trusted publication authority and acquisition plan.
func LoadDisplayMaskWithAuthority(path string, plan era5hourly.CellPlan, authority PublicationAuthority) (DisplayMask, error) {
	if path == "" || authority.acquisitionPlanSHA256 != plan.SHA256() ||
		authority.logicalPlanSHA256 != plan.LogicalSHA256() ||
		authority.landSourceSHA256 != plan.LandSourceSHA256() ||
		authority.displayMaskDerivation != era5hourly.CentreLandMaskDerivation ||
		!validTrustedSHA256(authority.landSourceSHA256) || !validTrustedSHA256(authority.displayMaskSHA256) ||
		!validTrustedSHA256(authority.sha256) ||
		authority.centreLandCells < 1 || authority.centreLandCells >= Rows*Columns {
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
	if actual != authority.displayMaskSHA256 {
		return DisplayMask{}, errors.New("ERA5 display mask checksum mismatch")
	}
	landCells := 0
	for index, value := range values {
		if value > 1 {
			return DisplayMask{}, fmt.Errorf("ERA5 display mask cell %d has value %d; want 0 or 1", index, value)
		}
		landCells += int(value)
	}
	if landCells != authority.centreLandCells {
		return DisplayMask{}, fmt.Errorf("ERA5 display mask has %d land centres; trusted authority requires %d", landCells, authority.centreLandCells)
	}
	return DisplayMask{
		values: values, sha256: actual, landSourceSHA256: authority.landSourceSHA256,
		derivation: authority.displayMaskDerivation, centreLandCells: authority.centreLandCells,
		acquisitionPlanSHA256:      authority.acquisitionPlanSHA256,
		logicalPlanSHA256:          authority.logicalPlanSHA256,
		publicationAuthorityKind:   sidecarAuthorityKind,
		publicationAuthoritySHA256: authority.sha256,
	}, nil
}

func (mask DisplayMask) matchesPlan(plan era5hourly.CellPlan) bool {
	if len(mask.values) != Rows*Columns || !validTrustedSHA256(mask.sha256) ||
		mask.acquisitionPlanSHA256 != plan.SHA256() ||
		mask.logicalPlanSHA256 != plan.LogicalSHA256() ||
		mask.landSourceSHA256 != plan.LandSourceSHA256() ||
		mask.derivation != era5hourly.CentreLandMaskDerivation ||
		!validTrustedSHA256(mask.publicationAuthoritySHA256) {
		return false
	}
	return mask.publicationAuthorityKind == sidecarAuthorityKind
}
