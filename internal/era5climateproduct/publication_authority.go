package era5climateproduct

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"nice-weather-finder/internal/providers/era5hourly"
)

const (
	publicationAuthorityVersion = 1
	maximumAuthorityBytes       = 4096

	inlineAuthorityKind  = "checksum-trusted-acquisition-plan"
	sidecarAuthorityKind = "checksum-trusted-publication-authority-v1"
)

// PublicationAuthority is a separately trusted augmentation for a legacy
// acquisition plan. It adds display authority without changing that plan or
// the daily shards which bind its compressed checksum.
type PublicationAuthority struct {
	sha256                string
	acquisitionPlanSHA256 string
	logicalPlanSHA256     string
	landSourceSHA256      string
	displayMaskDerivation string
	displayMaskSHA256     string
	centreLandCells       int
}

type encodedPublicationAuthority struct {
	SchemaVersion         int    `json:"schemaVersion"`
	AcquisitionPlanSHA256 string `json:"acquisitionPlanSha256"`
	LogicalPlanSHA256     string `json:"logicalPlanSha256"`
	Rows                  int    `json:"rows"`
	Columns               int    `json:"columns"`
	CellRegistration      string `json:"cellRegistration"`
	LandSourceSHA256      string `json:"landSourceSha256"`
	DisplayMaskAuthority  string `json:"displayMaskAuthority"`
	DisplayMaskDerivation string `json:"displayMaskDerivation"`
	DisplayMaskSHA256     string `json:"displayMaskSha256"`
	DisplayMaskBytes      int    `json:"displayMaskBytes"`
	CentreLandCells       int    `json:"centreLandCells"`
}

// LoadPublicationAuthority validates a small sidecar against a separately
// trusted checksum and the exact already-validated acquisition plan.
func LoadPublicationAuthority(path, expectedSHA256 string, plan era5hourly.CellPlan) (PublicationAuthority, error) {
	var result PublicationAuthority
	if !validTrustedSHA256(expectedSHA256) {
		return result, errors.New("ERA5 publication authority requires a trusted lowercase SHA-256")
	}
	if _, _, _, _, inline := plan.DisplayMaskAuthority(); inline {
		return result, errors.New("ERA5 publication authority cannot replace authority already bound into the acquisition plan")
	}
	body, err := readBounded(path, maximumAuthorityBytes)
	if err != nil {
		return result, fmt.Errorf("read ERA5 publication authority: %w", err)
	}
	digest := sha256.Sum256(body)
	actualSHA256 := hex.EncodeToString(digest[:])
	if actualSHA256 != expectedSHA256 {
		return result, errors.New("ERA5 publication authority checksum mismatch")
	}
	var encoded encodedPublicationAuthority
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&encoded); err != nil {
		return result, fmt.Errorf("decode ERA5 publication authority: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return result, errors.New("ERA5 publication authority has trailing JSON")
	}
	if encoded.SchemaVersion != publicationAuthorityVersion ||
		encoded.AcquisitionPlanSHA256 != plan.SHA256() ||
		encoded.LogicalPlanSHA256 != plan.LogicalSHA256() ||
		encoded.Rows != Rows || encoded.Columns != Columns ||
		encoded.CellRegistration != "exact-era5-grid-point-centres" ||
		encoded.LandSourceSHA256 != plan.LandSourceSHA256() ||
		encoded.DisplayMaskAuthority != DisplayMaskAuthority ||
		encoded.DisplayMaskDerivation != era5hourly.CentreLandMaskDerivation ||
		encoded.DisplayMaskBytes != Rows*Columns ||
		encoded.CentreLandCells != plan.CentreLandCellCount() ||
		encoded.CentreLandCells < 1 || encoded.CentreLandCells >= Rows*Columns ||
		!validTrustedSHA256(encoded.DisplayMaskSHA256) {
		return result, errors.New("ERA5 publication authority does not match its acquisition plan or canonical display contract")
	}
	return PublicationAuthority{
		sha256:                actualSHA256,
		acquisitionPlanSHA256: encoded.AcquisitionPlanSHA256,
		logicalPlanSHA256:     encoded.LogicalPlanSHA256,
		landSourceSHA256:      encoded.LandSourceSHA256,
		displayMaskDerivation: encoded.DisplayMaskDerivation,
		displayMaskSHA256:     encoded.DisplayMaskSHA256,
		centreLandCells:       encoded.CentreLandCells,
	}, nil
}

func validTrustedSHA256(value string) bool {
	return validSHA256(value) && value == strings.ToLower(value)
}
