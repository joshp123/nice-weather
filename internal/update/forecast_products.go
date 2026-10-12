package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"nice-weather-finder/internal/forecasttiles"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"nice-weather-finder/internal/forecastcatalog"
	"nice-weather-finder/internal/forecastrange"
	"nice-weather-finder/internal/forecastruns"
)

// RebuildForecastProducts reuses immutable daily map records and publishes new derived ranges.
func RebuildForecastProducts(ctx context.Context, artifactRoot, sourceID, indexID string) error {
	if sourceID == "" || indexID == "" || sourceID == indexID || strings.ContainsAny(sourceID+indexID, "/\\") || strings.Contains(sourceID, "..") || strings.Contains(indexID, "..") {
		return errors.New("forecast rebuild requires distinct safe source and publication identities")
	}
	root, err := filepath.Abs(artifactRoot)
	if err != nil {
		return err
	}
	forecastRoot := filepath.Join(root, "forecast")
	current, err := os.ReadFile(filepath.Join(forecastRoot, "current.json"))
	if err != nil {
		return err
	}
	var pointer forecastruns.Pointer
	if err := json.Unmarshal(current, &pointer); err != nil || pointer.Current.IndexID != sourceID {
		return errors.New("forecast rebuild must name the active source snapshot")
	}
	sourceIndexRaw, err := os.ReadFile(filepath.Join(forecastRoot, filepath.FromSlash(pointer.Current.Path)))
	if err != nil {
		return err
	}
	sourceIndexHash := sha256.Sum256(sourceIndexRaw)
	if hex.EncodeToString(sourceIndexHash[:]) != pointer.Current.SHA256 {
		return errors.New("source forecast index checksum mismatch")
	}
	var sourceIndex forecastruns.Index
	if err := json.Unmarshal(sourceIndexRaw, &sourceIndex); err != nil {
		return err
	}
	authorizedCatalogs := make(map[string]string)
	for _, day := range sourceIndex.Days {
		for _, tier := range day.Tiers {
			authorizedCatalogs[strings.TrimPrefix(tier.CatalogPath, "runs/"+sourceID+"/")] = tier.CatalogSHA256
		}
	}

	source := filepath.Join(forecastRoot, "runs", sourceID, "forecast-days")
	candidate := filepath.Join(forecastRoot, "runs", indexID)
	if err := os.Mkdir(candidate, 0755); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(candidate, "forecast-days", relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("daily reuse source contains a symlink")
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		if !info.Mode().IsRegular() {
			return errors.New("daily reuse source is not a regular file")
		}
		if entry.Name() == "catalog.json" {
			payload, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			hash := sha256.Sum256(payload)
			catalogPath := filepath.ToSlash(filepath.Join("forecast-days", relative))
			if authorizedCatalogs[catalogPath] != hex.EncodeToString(hash[:]) {
				return errors.New("source daily catalog checksum mismatch")
			}
			if _, err := forecastcatalog.Parse(payload); err != nil {
				return err
			}
		} else if filepath.Ext(path) != ".nwt" && filepath.Ext(path) != ".nws" {
			return errors.New("unexpected source daily asset")
		}

		if err := os.Link(path, target); err != nil {
			if !errors.Is(err, os.ErrExist) {
				return err
			}
			existing, err := os.Stat(target)
			if err != nil || !os.SameFile(info, existing) {
				return errors.New("range candidate already contains different daily bytes")
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	global, regional, err := installedRangeInputs(candidate)
	if err != nil {
		return err
	}
	for _, inputs := range [][]forecastrange.InputCatalog{nil, regional} {
		if inputs != nil && len(inputs) == 0 {
			continue
		}
		result, err := forecastrange.Build(ctx, forecastrange.Config{Root: candidate, ReleaseID: indexID, Inputs: global, RegionalInputs: inputs})
		if err != nil {
			return err
		}
		fmt.Printf("rebuilt %s range: %d non-empty blocks, %d scalar tiles, %d bytes\n", result.Catalog.Tier(), result.Catalog.Coverage.AvailablePointBlocks, result.Catalog.Coverage.ScalarTiles, result.Stats.OutputBytes)
	}
	_, err = forecastruns.PublishRebuilt(ctx, root, indexID, sourceID)
	return err
}

func installedRangeInputs(root string) ([]forecastrange.InputCatalog, []forecastrange.InputCatalog, error) {
	entries, err := os.ReadDir(filepath.Join(root, "forecast-days"))
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var global, regional []forecastrange.InputCatalog
	for _, entry := range entries {
		for _, tier := range []string{forecasttiles.TierGlobal, forecasttiles.TierEurope} {
			relative := filepath.ToSlash(filepath.Join("forecast-days", entry.Name(), tier, "catalog.json"))
			payload, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
			if errors.Is(err, os.ErrNotExist) && tier == forecasttiles.TierEurope {
				continue
			}
			if err != nil {
				return nil, nil, err
			}
			if _, err := forecastcatalog.Parse(payload); err != nil {
				return nil, nil, err
			}
			digest := sha256.Sum256(payload)
			reference := forecastrange.InputCatalog{Date: entry.Name(), Path: relative, Bytes: uint64(len(payload)), SHA256: hex.EncodeToString(digest[:])}
			if tier == forecasttiles.TierGlobal {
				global = append(global, reference)
			} else {
				regional = append(regional, reference)
			}
		}
	}
	return global, regional, nil
}
