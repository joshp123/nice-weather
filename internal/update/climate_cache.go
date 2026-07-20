package update

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nice-weather-finder/internal/delivery"
)

// Climate checkpoints are derived artifacts of the complete product
// semantics. A product-contract bump must invalidate them automatically.
const climateScoreContract = delivery.ProductContract + "|chelsa-2.1-1981-2010-0p1|validated-finite|overlap-renderer-v2"

type climateCheckpoint struct {
	Contract string                 `json:"contract"`
	Period   delivery.ClimatePeriod `json:"period"`
}

func saveClimateCheckpoint(cacheRoot, releaseDir string, period delivery.ClimatePeriod) error {
	for _, relative := range periodAssets(period.Period) {
		if err := copyFile(filepath.Join(releaseDir, filepath.FromSlash(relative)), filepath.Join(cacheRoot, filepath.FromSlash(relative))); err != nil {
			return err
		}
	}
	checkpoint := climateCheckpoint{Contract: climateScoreContract, Period: period}
	data, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(cacheRoot, ".checkpoint-*.tmp")
	if err != nil {
		return err
	}
	path := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(path)
		}
	}()
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	final := filepath.Join(cacheRoot, fmt.Sprintf("month-%02d.json", period.Month))
	if err := os.Rename(path, final); err != nil {
		return err
	}
	committed = true
	return nil
}

func loadClimateCheckpoint(cacheRoot, releaseDir string, productBounds delivery.GridBounds, month int) (delivery.ClimatePeriod, bool, error) {
	data, err := os.ReadFile(filepath.Join(cacheRoot, fmt.Sprintf("month-%02d.json", month)))
	if os.IsNotExist(err) {
		return delivery.ClimatePeriod{}, false, nil
	}
	if err != nil {
		return delivery.ClimatePeriod{}, false, err
	}
	var checkpoint climateCheckpoint
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&checkpoint); err != nil {
		return invalidateClimateCheckpoint(cacheRoot, month, fmt.Errorf("parse climate checkpoint: %w", err))
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("trailing JSON")
		}
		return invalidateClimateCheckpoint(cacheRoot, month, fmt.Errorf("parse climate checkpoint: %w", err))
	}
	period := checkpoint.Period
	if checkpoint.Contract != climateScoreContract {
		if err := removeClimateCheckpointAssets(cacheRoot, period.Period); err != nil {
			return delivery.ClimatePeriod{}, false, fmt.Errorf("remove obsolete climate checkpoint assets: %w", err)
		}
		return invalidateClimateCheckpoint(cacheRoot, month, fmt.Errorf("climate month %02d checkpoint renderer contract is obsolete", month))
	}
	if period.Month != month || period.ID != fmt.Sprintf("month-%02d", month) ||
		period.Label != time.Month(month).String() || period.Source.ProviderID != "chelsa" || period.Source.ProviderLabel != "CHELSA 2.1 · CC0" ||
		period.Source.RunAt != "1981-01-01T00:00:00Z" || period.Source.ValidFrom != "1981-01-01T00:00:00Z" || period.Source.ValidTo != "2011-01-01T00:00:00Z" ||
		period.NativeResolutionKM != 11 || period.TemporalResolutionHours != 720 || period.Point.URL == "" || len(period.Images) != 2 || len(period.Lenses) != len(delivery.RequiredLensAssets()) {
		return invalidateClimateCheckpoint(cacheRoot, month, fmt.Errorf("climate month %02d checkpoint contract is invalid", month))
	}
	if !safeCacheAsset(period.Point.URL) || !strings.Contains(filepath.Base(period.Point.URL), period.Point.SHA256) {
		return invalidateClimateCheckpoint(cacheRoot, month, fmt.Errorf("climate month %02d cached point path is unsafe", month))
	}
	for _, profile := range []string{delivery.ProfileNice, delivery.ProfileCycling} {
		asset, ok := period.Images[profile]
		if !ok || !safeCacheAsset(asset.URL) || !strings.Contains(filepath.Base(asset.URL), asset.SHA256) {
			return invalidateClimateCheckpoint(cacheRoot, month, fmt.Errorf("climate month %02d cached image contract is invalid", month))
		}
	}
	for _, lens := range delivery.RequiredLensAssets() {
		asset, ok := period.Lenses[lens]
		if !ok || !safeCacheAsset(asset.URL) || !strings.Contains(filepath.Base(asset.URL), asset.SHA256) {
			return invalidateClimateCheckpoint(cacheRoot, month, fmt.Errorf("climate month %02d cached lens contract is invalid", month))
		}
	}
	pointPath := filepath.Join(cacheRoot, filepath.FromSlash(period.Point.URL))
	metadata, err := delivery.ValidatePointAsset(pointPath)
	if err != nil || metadata.SHA256 != period.Point.SHA256 || metadata.PayloadSHA256 != period.Point.PayloadSHA256 || metadata.Geometry != period.Point.Geometry || metadata.Geometry.Bounds != productBounds || metadata.Kind != delivery.PointKindLegacyClimateMonth {
		return invalidateClimateCheckpoint(cacheRoot, month, fmt.Errorf("climate month %02d cached point asset is invalid", month))
	}
	for _, image := range period.Images {
		if err := validateCachedImage(filepath.Join(cacheRoot, filepath.FromSlash(image.URL)), image); err != nil {
			return invalidateClimateCheckpoint(cacheRoot, month, err)
		}
	}
	for _, image := range period.Lenses {
		if err := validateCachedImage(filepath.Join(cacheRoot, filepath.FromSlash(image.URL)), image); err != nil {
			return invalidateClimateCheckpoint(cacheRoot, month, err)
		}
	}
	for _, relative := range periodAssets(period.Period) {
		if err := copyFile(filepath.Join(cacheRoot, filepath.FromSlash(relative)), filepath.Join(releaseDir, filepath.FromSlash(relative))); err != nil {
			return delivery.ClimatePeriod{}, false, err
		}
	}
	return period, true, nil
}

func removeClimateCheckpointAssets(cacheRoot string, period delivery.Period) error {
	for _, relative := range periodAssets(period) {
		if safeCacheAsset(relative) {
			path := filepath.Join(cacheRoot, filepath.FromSlash(relative))
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove %q: %w", relative, err)
			}
		}
	}
	return nil
}

func safeCacheAsset(relative string) bool {
	return relative != "" && !filepath.IsAbs(relative) && filepath.ToSlash(filepath.Clean(relative)) == relative && relative != "." && !strings.HasPrefix(relative, "../") && !strings.Contains(relative, "\\")
}

func invalidateClimateCheckpoint(cacheRoot string, month int, cause error) (delivery.ClimatePeriod, bool, error) {
	path := filepath.Join(cacheRoot, fmt.Sprintf("month-%02d.json", month))
	// Keep one diagnostic copy, not one copy per failed run. The climate cache
	// is persistent and must remain bounded even when a checkpoint stays bad.
	quarantine := path + ".invalid"
	_ = os.Remove(quarantine)
	if err := os.Rename(path, quarantine); err != nil && !os.IsNotExist(err) {
		return delivery.ClimatePeriod{}, false, fmt.Errorf("quarantine invalid climate checkpoint: %w", err)
	}
	fmt.Printf("warning: rebuilding invalid climate month %02d checkpoint: %v\n", month, cause)
	return delivery.ClimatePeriod{}, false, nil
}

func periodAssets(period delivery.Period) []string {
	result := []string{period.Point.URL}
	for _, profile := range []string{delivery.ProfileNice, delivery.ProfileCycling} {
		if asset, ok := period.Images[profile]; ok {
			result = append(result, asset.URL)
		}
	}
	for _, lens := range delivery.RequiredLensAssets() {
		if asset, ok := period.Lenses[lens]; ok {
			result = append(result, asset.URL)
		}
	}
	return result
}

func validateCachedImage(path string, asset delivery.ImageAsset) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return err
	}
	if hex.EncodeToString(digest.Sum(nil)) != asset.SHA256 {
		return fmt.Errorf("cached image %q checksum mismatch", asset.URL)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	config, err := png.DecodeConfig(file)
	if err != nil || config.Width != asset.Width || config.Height != asset.Height {
		return fmt.Errorf("cached image %q geometry is invalid", asset.URL)
	}
	return nil
}

func copyFile(source, destination string) (err error) {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	output, err := os.CreateTemp(filepath.Dir(destination), ".copy-*.tmp")
	if err != nil {
		return err
	}
	temporary := output.Name()
	committed := false
	defer func() {
		_ = output.Close()
		if !committed {
			_ = os.Remove(temporary)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, destination); err != nil {
		return err
	}
	committed = true
	return nil
}
