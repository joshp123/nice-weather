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

type displayAuthority struct {
	landSourceSHA256           string
	derivation                 string
	maskSHA256                 string
	centreLandCells            int
	acquisitionPlanSHA256      string
	logicalPlanSHA256          string
	publicationAuthorityKind   string
	publicationAuthoritySHA256 string
}

// LoadDisplayMask accepts the one canonical 721x1440 centre-on-land plane
// whose digest and Natural Earth source are bound into the trusted cell plan.
func LoadDisplayMask(path string, plan era5hourly.CellPlan) (DisplayMask, error) {
	landSourceSHA256, derivation, expectedSHA256, centreLandCells, ok := plan.DisplayMaskAuthority()
	if !ok {
		return DisplayMask{}, errors.New("ERA5 display mask requires canonical authority from its trusted cell plan")
	}
	return loadDisplayMask(path, plan, displayAuthority{
		landSourceSHA256: landSourceSHA256, derivation: derivation,
		maskSHA256: expectedSHA256, centreLandCells: centreLandCells,
		acquisitionPlanSHA256: plan.SHA256(), logicalPlanSHA256: plan.LogicalSHA256(),
		publicationAuthorityKind: inlineAuthorityKind, publicationAuthoritySHA256: plan.SHA256(),
	})
}

// LoadDisplayMaskWithAuthority accepts a legacy plan only when a separately
// checksum-trusted authority binds that exact plan and the canonical mask.
func LoadDisplayMaskWithAuthority(path string, plan era5hourly.CellPlan, authority PublicationAuthority) (DisplayMask, error) {
	return loadDisplayMask(path, plan, displayAuthority{
		landSourceSHA256:           authority.landSourceSHA256,
		derivation:                 authority.displayMaskDerivation,
		maskSHA256:                 authority.displayMaskSHA256,
		centreLandCells:            authority.centreLandCells,
		acquisitionPlanSHA256:      authority.acquisitionPlanSHA256,
		logicalPlanSHA256:          authority.logicalPlanSHA256,
		publicationAuthorityKind:   sidecarAuthorityKind,
		publicationAuthoritySHA256: authority.sha256,
	})
}

func loadDisplayMask(path string, plan era5hourly.CellPlan, authority displayAuthority) (DisplayMask, error) {
	if path == "" || authority.acquisitionPlanSHA256 != plan.SHA256() ||
		authority.logicalPlanSHA256 != plan.LogicalSHA256() ||
		authority.landSourceSHA256 != plan.LandSourceSHA256() ||
		authority.derivation != era5hourly.CentreLandMaskDerivation ||
		!validTrustedSHA256(authority.landSourceSHA256) || !validTrustedSHA256(authority.maskSHA256) ||
		!validTrustedSHA256(authority.publicationAuthoritySHA256) ||
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
	if actual != authority.maskSHA256 {
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
		derivation: authority.derivation, centreLandCells: authority.centreLandCells,
		acquisitionPlanSHA256:      authority.acquisitionPlanSHA256,
		logicalPlanSHA256:          authority.logicalPlanSHA256,
		publicationAuthorityKind:   authority.publicationAuthorityKind,
		publicationAuthoritySHA256: authority.publicationAuthoritySHA256,
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
	if mask.publicationAuthorityKind == sidecarAuthorityKind {
		return true
	}
	landSourceSHA256, derivation, maskSHA256, centreLandCells, ok := plan.DisplayMaskAuthority()
	return mask.publicationAuthorityKind == inlineAuthorityKind && ok &&
		mask.publicationAuthoritySHA256 == plan.SHA256() && mask.sha256 == maskSHA256 &&
		mask.landSourceSHA256 == landSourceSHA256 && mask.derivation == derivation &&
		mask.centreLandCells == centreLandCells
}
