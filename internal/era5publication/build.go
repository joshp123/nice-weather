// Package era5publication generates the checksum-trusted display authority
// required to publish climate products from ERA5 acquisition plans.
package era5publication

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"nice-weather-finder/internal/era5climateproduct"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/providers/era5hourly"
	weatherupdate "nice-weather-finder/internal/update"
)

const (
	DisplayMaskFilename          = "display-mask.bin"
	PublicationAuthorityFilename = "publication-authority.json"
	landLayer                    = "ne_10m_land"
	landFilter                   = "featurecla IS NULL OR featurecla = 'Land'"
	gridDegrees                  = 0.25
	firstLatitude                = 90.0
	firstLongitude               = -180.0
)

type Config struct {
	PlanPath      string
	PlanSHA256    string
	LandCacheRoot string
	OutputRoot    string
}

type Summary struct {
	OutputRoot                 string
	DisplayMaskPath            string
	PublicationAuthorityPath   string
	DisplayMaskSHA256          string
	PublicationAuthoritySHA256 string
	CentreLandCells            int
	Existing                   bool
}

func Build(ctx context.Context, config Config) (Summary, error) {
	return build(ctx, config, pipeline.ExecRunner{}, preparePinnedLand)
}

type landPreparer func(context.Context, string, pipeline.CommandRunner) (string, error)

func preparePinnedLand(ctx context.Context, cacheRoot string, runner pipeline.CommandRunner) (string, error) {
	return weatherupdate.NewLandAuthority(cacheRoot, nil, runner).Prepare(ctx)
}

func build(ctx context.Context, config Config, runner pipeline.CommandRunner, prepare landPreparer) (summary Summary, err error) {
	if ctx == nil || runner == nil || prepare == nil {
		return summary, errors.New("ERA5 publication authority dependencies are unavailable")
	}
	for _, candidate := range []struct {
		name string
		path string
	}{
		{"plan", config.PlanPath},
		{"land cache", config.LandCacheRoot},
		{"output", config.OutputRoot},
	} {
		if candidate.path == "" || !filepath.IsAbs(candidate.path) || filepath.Clean(candidate.path) != candidate.path || candidate.path == string(filepath.Separator) {
			return summary, fmt.Errorf("ERA5 publication authority %s must be a clean absolute path other than the filesystem root", candidate.name)
		}
	}
	plan, err := era5hourly.LoadCellPlan(config.PlanPath, config.PlanSHA256)
	if err != nil {
		return summary, err
	}
	if plan.LandSourceSHA256() != weatherupdate.LandSourceSHA256 {
		return summary, fmt.Errorf("ERA5 acquisition plan land source %s does not match pinned Natural Earth %s", plan.LandSourceSHA256(), weatherupdate.LandSourceSHA256)
	}
	parent := filepath.Dir(config.OutputRoot)
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return summary, fmt.Errorf("inspect ERA5 publication authority output parent: %w", err)
	}
	if !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return summary, errors.New("ERA5 publication authority output parent must be an existing real directory")
	}

	shapePath, err := prepare(ctx, config.LandCacheRoot, runner)
	if err != nil {
		return summary, err
	}
	mask, err := rasterizeExactCentres(ctx, runner, shapePath)
	if err != nil {
		return summary, err
	}
	if err := validateGeography(mask); err != nil {
		return summary, err
	}
	authorityArtifact, err := era5climateproduct.EncodePublicationAuthority(plan, mask)
	if err != nil {
		return summary, err
	}

	stagedRoot, err := os.MkdirTemp(parent, ".era5-publication-authority-")
	if err != nil {
		return summary, err
	}
	defer func() {
		if stagedRoot != "" {
			err = errors.Join(err, os.RemoveAll(stagedRoot))
		}
	}()
	maskPath := filepath.Join(stagedRoot, DisplayMaskFilename)
	authorityPath := filepath.Join(stagedRoot, PublicationAuthorityFilename)
	if err := writeExclusive(maskPath, mask); err != nil {
		return summary, err
	}
	if err := writeExclusive(authorityPath, authorityArtifact.JSON); err != nil {
		return summary, err
	}
	authority, err := era5climateproduct.LoadPublicationAuthority(authorityPath, authorityArtifact.SHA256, plan)
	if err != nil {
		return summary, fmt.Errorf("reload staged ERA5 publication authority: %w", err)
	}
	if _, err := era5climateproduct.LoadDisplayMaskWithAuthority(maskPath, plan, authority); err != nil {
		return summary, fmt.Errorf("reload staged ERA5 display mask: %w", err)
	}

	summary = Summary{
		OutputRoot:                 config.OutputRoot,
		DisplayMaskPath:            filepath.Join(config.OutputRoot, DisplayMaskFilename),
		PublicationAuthorityPath:   filepath.Join(config.OutputRoot, PublicationAuthorityFilename),
		DisplayMaskSHA256:          authorityArtifact.DisplayMaskSHA256,
		PublicationAuthoritySHA256: authorityArtifact.SHA256,
		CentreLandCells:            authorityArtifact.CentreLandCells,
	}
	if same, compareErr := outputMatches(config.OutputRoot, mask, authorityArtifact.JSON); compareErr != nil {
		return Summary{}, compareErr
	} else if same {
		summary.Existing = true
		return summary, nil
	}
	if _, statErr := os.Lstat(config.OutputRoot); statErr == nil {
		return Summary{}, errors.New("refuse to overwrite different ERA5 publication authority output")
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return Summary{}, fmt.Errorf("inspect ERA5 publication authority output: %w", statErr)
	}
	if err := os.Rename(stagedRoot, config.OutputRoot); err != nil {
		if same, compareErr := outputMatches(config.OutputRoot, mask, authorityArtifact.JSON); compareErr == nil && same {
			summary.Existing = true
			return summary, nil
		}
		return Summary{}, fmt.Errorf("install ERA5 publication authority: %w", err)
	}
	stagedRoot = ""
	return summary, nil
}

func rasterizeExactCentres(ctx context.Context, runner pipeline.CommandRunner, shapePath string) ([]byte, error) {
	temporary, err := os.MkdirTemp("", "nice-weather-era5-centres-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temporary)
	output := filepath.Join(temporary, "mask.bil")
	half := gridDegrees / 2
	west := firstLongitude - half
	east := firstLongitude + gridDegrees*era5climateproduct.Columns - half
	north := firstLatitude + half
	south := firstLatitude - gridDegrees*era5climateproduct.Rows + half
	_, err = runner.Run(ctx, "gdal_rasterize",
		"-q", "-of", "EHdr", "-ot", "Byte", "-burn", "1", "-init", "0",
		"-te", coordinate(west), coordinate(south), coordinate(east), coordinate(north),
		"-ts", strconv.Itoa(era5climateproduct.Columns), strconv.Itoa(era5climateproduct.Rows),
		"-where", landFilter, "-l", landLayer, shapePath, output,
	)
	if err != nil {
		return nil, fmt.Errorf("rasterize Natural Earth at exact ERA5 centres: %w", err)
	}
	mask, err := os.ReadFile(output)
	if err != nil {
		return nil, fmt.Errorf("read exact ERA5 centre mask: %w", err)
	}
	if len(mask) != era5climateproduct.Rows*era5climateproduct.Columns {
		return nil, fmt.Errorf("exact ERA5 centre mask has %d bytes; want %d", len(mask), era5climateproduct.Rows*era5climateproduct.Columns)
	}
	for index, value := range mask {
		if value > 1 {
			return nil, fmt.Errorf("exact ERA5 centre mask cell %d has value %d; want 0 or 1", index, value)
		}
	}
	return mask, nil
}

func validateGeography(mask []byte) error {
	for _, probe := range []struct {
		name                string
		latitude, longitude float64
		want                byte
	}{
		{"Amsterdam", 52.5, 5, 1},
		{"Malaga", 36.75, -4.5, 1},
		{"north Atlantic", 45, -15, 0},
		{"Gulf of Guinea", 0, 0, 0},
		{"west antimeridian at equator", 0, -180, 0},
		{"east antimeridian at equator", 0, 179.75, 0},
	} {
		index, ok := exactCentreIndex(probe.latitude, probe.longitude)
		if !ok || mask[index] != probe.want {
			return fmt.Errorf("exact ERA5 centre mask %s probe is %d; want %d", probe.name, mask[index], probe.want)
		}
	}
	return nil
}

func exactCentreIndex(latitude, longitude float64) (int, bool) {
	row := int((firstLatitude - latitude) / gridDegrees)
	column := int((longitude - firstLongitude) / gridDegrees)
	if row < 0 || row >= era5climateproduct.Rows || column < 0 || column >= era5climateproduct.Columns ||
		firstLatitude-gridDegrees*float64(row) != latitude || firstLongitude+gridDegrees*float64(column) != longitude {
		return 0, false
	}
	return row*era5climateproduct.Columns + column, true
}

func outputMatches(root string, mask, authority []byte) (bool, error) {
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("existing ERA5 publication authority output is not a real directory")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return false, err
	}
	if len(entries) != 2 {
		return false, nil
	}
	for name, expected := range map[string][]byte{
		DisplayMaskFilename:          mask,
		PublicationAuthorityFilename: authority,
	} {
		path := filepath.Join(root, name)
		fileInfo, err := os.Lstat(path)
		if err != nil || !fileInfo.Mode().IsRegular() || fileInfo.Mode()&os.ModeSymlink != 0 || fileInfo.Size() != int64(len(expected)) {
			return false, nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return false, err
		}
		if !bytes.Equal(body, expected) {
			return false, nil
		}
	}
	return true, nil
}

func writeExclusive(path string, body []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	written, writeErr := file.Write(body)
	if writeErr == nil && written != len(body) {
		writeErr = io.ErrShortWrite
	}
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}

func coordinate(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }
