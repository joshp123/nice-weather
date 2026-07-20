// Command scalarcanary publishes a disposable browser proof from one current
// release. It never changes the weather release pointer or the production UI.
package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/tiledelivery"
)

const canaryContract = "nice-weather-scalar-canary-v2"

type config struct {
	artifacts string
	web       string
	period    string
	minZoom   int
	maxZoom   int
}

type canaryIndex struct {
	Contract           string                 `json:"contract"`
	ReleaseID          string                 `json:"releaseId"`
	DataStatus         delivery.DataStatus    `json:"dataStatus"`
	PeriodID           string                 `json:"periodId"`
	PeriodLabel        string                 `json:"periodLabel"`
	SourceLabel        string                 `json:"sourceLabel"`
	GeneratedAt        string                 `json:"generatedAt"`
	MinZoom            int                    `json:"minZoom"`
	MaxZoom            int                    `json:"maxZoom"`
	TileCount          int                    `json:"tileCount"`
	DecodedBytes       uint64                 `json:"decodedBytes"`
	EstimatedGzipBytes uint64                 `json:"estimatedGzipBytes"`
	TileTemplate       string                 `json:"tileTemplate"`
	Palette            []delivery.PaletteStop `json:"palette"`
	Tiles              []canaryTile           `json:"tiles"`
	Probes             []canaryProbe          `json:"probes"`
}

type canaryTile struct {
	Zoom         uint8  `json:"z"`
	X            uint32 `json:"x"`
	Y            uint32 `json:"y"`
	URL          string `json:"url"`
	SHA256       string `json:"sha256"`
	Decoded      uint64 `json:"decodedBytes"`
	Gzip         uint64 `json:"estimatedGzipBytes"`
	DataTexels   uint32 `json:"dataTexels"`
	NoDataTexels uint32 `json:"noDataTexels"`
	path         string
}

type canaryProbe struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Offset int    `json:"offset"`
	Score  uint8  `json:"score"`
}

type scoreSource struct {
	reader  *delivery.PointAssetReader
	display bool
}

func main() {
	settings := config{}
	flag.StringVar(&settings.artifacts, "artifacts", "./artifacts", "weather artifact root")
	flag.StringVar(&settings.web, "web", "./dist", "built web root")
	flag.StringVar(&settings.period, "period", "", "forecast day ID; the first day is used when empty")
	flag.IntVar(&settings.minZoom, "min-zoom", 0, "minimum scalar tile zoom")
	flag.IntVar(&settings.maxZoom, "max-zoom", 5, "maximum scalar tile zoom")
	flag.Parse()
	if err := run(settings); err != nil {
		fmt.Fprintln(os.Stderr, "scalar canary:", err)
		os.Exit(1)
	}
}

func run(settings config) error {
	if settings.minZoom < 0 || settings.maxZoom < settings.minZoom || settings.maxZoom > tiledelivery.MaxZoom {
		return errors.New("invalid zoom range")
	}
	current, ok := delivery.CurrentDir(settings.artifacts)
	if !ok {
		return errors.New("current weather release is missing")
	}
	current, err := filepath.EvalSymlinks(current)
	if err != nil {
		return fmt.Errorf("resolve current weather release: %w", err)
	}
	manifest, err := delivery.ValidateProductionRelease(current)
	if err != nil {
		return fmt.Errorf("validate current release: %w", err)
	}
	period, err := selectPeriod(manifest, settings.period)
	if err != nil {
		return err
	}
	point := period.Point
	display := false
	if period.DisplayPoint != nil {
		point = *period.DisplayPoint
		display = true
	}
	pointPath := filepath.Join(current, filepath.FromSlash(point.URL))
	reader, err := delivery.OpenPointAsset(pointPath)
	if err != nil {
		return fmt.Errorf("open evaluated point evidence: %w", err)
	}
	defer reader.Close()
	source := scoreSource{reader: reader, display: display}
	sampler := func(sample tiledelivery.ScalarSample) (uint8, bool, error) {
		return source.niceScore(sample.Longitude, sample.Latitude)
	}

	assetRoot := filepath.Join(settings.web, "assets", "scalar-canary", canaryContract)
	writer, err := tiledelivery.NewWriter(assetRoot, manifest.ReleaseID)
	if err != nil {
		return err
	}
	index := canaryIndex{
		Contract: canaryContract, ReleaseID: manifest.ReleaseID, DataStatus: manifest.DataStatus,
		PeriodID: period.ID, PeriodLabel: period.Label, SourceLabel: period.Source.ProviderLabel,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339), MinZoom: settings.minZoom, MaxZoom: settings.maxZoom,
		TileTemplate: fmt.Sprintf("/assets/scalar-canary/%s/releases/%s/scores/%s/europe/{z}/{x}/{y}.nws", canaryContract, manifest.ReleaseID, period.ID),
		Palette:      manifest.Palette,
	}
	assets := make(map[string]canaryTile)
	for zoom := settings.minZoom; zoom <= settings.maxZoom; zoom++ {
		ids, err := coveringIDs(manifest.Region.Bounds, uint8(zoom))
		if err != nil {
			return err
		}
		for _, id := range ids {
			asset, err := writeOrReuseScalar(writer, assetRoot, manifest.ReleaseID, id, period.ID, sampler)
			if err != nil {
				return fmt.Errorf("write scalar tile %d/%d/%d: %w", zoom, id.X, id.Y, err)
			}
			if asset.Omitted {
				continue
			}
			encoded, err := os.ReadFile(asset.Path)
			if err != nil {
				return err
			}
			gzipBytes, err := compressedSize(encoded)
			if err != nil {
				return err
			}
			url := fmt.Sprintf("/assets/scalar-canary/%s/releases/%s/scores/%s/europe/%d/%d/%d.nws", canaryContract, manifest.ReleaseID, period.ID, zoom, id.X, id.Y)
			tile := canaryTile{Zoom: id.Zoom, X: id.X, Y: id.Y, URL: url, SHA256: asset.SHA256, Decoded: asset.Bytes, Gzip: gzipBytes, DataTexels: asset.DataTexels, NoDataTexels: asset.NoDataTexels, path: asset.Path}
			index.Tiles = append(index.Tiles, tile)
			assets[tileKey(id)] = tile
			index.DecodedBytes += asset.Bytes
			index.EstimatedGzipBytes += gzipBytes
		}
	}
	index.TileCount = len(index.Tiles)
	sort.Slice(index.Tiles, func(i, j int) bool {
		if index.Tiles[i].Zoom != index.Tiles[j].Zoom {
			return index.Tiles[i].Zoom < index.Tiles[j].Zoom
		}
		if index.Tiles[i].Y != index.Tiles[j].Y {
			return index.Tiles[i].Y < index.Tiles[j].Y
		}
		return index.Tiles[i].X < index.Tiles[j].X
	})
	index.Probes, err = buildProbes(source, assets, uint8(settings.maxZoom))
	if err != nil {
		return err
	}
	if len(index.Probes) < 3 {
		return fmt.Errorf("only %d browser parity probes have evidence; want at least 3", len(index.Probes))
	}
	if err := writeIndex(filepath.Join(settings.web, "scalar-canary-index.json"), index); err != nil {
		return err
	}
	fmt.Printf("scalar canary: release=%s period=%s tiles=%d decoded=%d gzip=%d probes=%d\n", index.ReleaseID, index.PeriodID, index.TileCount, index.DecodedBytes, index.EstimatedGzipBytes, len(index.Probes))
	return nil
}

func writeOrReuseScalar(writer *tiledelivery.Writer, root, releaseID string, id tiledelivery.ID, period string, sampler tiledelivery.EvaluatedScoreSampler) (tiledelivery.ScalarAsset, error) {
	path := filepath.Join(root, "releases", releaseID, "scores", period, id.Tier, fmt.Sprint(id.Zoom), fmt.Sprint(id.X), fmt.Sprintf("%d.nws", id.Y))
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return writer.WriteScalar(id, period, sampler)
	} else if err != nil {
		return tiledelivery.ScalarAsset{}, err
	}
	encoded, stats, err := tiledelivery.BuildScalarTile(id, sampler)
	if err != nil {
		return tiledelivery.ScalarAsset{}, err
	}
	if stats.DataTexels == 0 {
		return tiledelivery.ScalarAsset{}, errors.New("existing scalar tile now has no evaluated data")
	}
	digest := sha256.Sum256(encoded)
	digestHex := hex.EncodeToString(digest[:])
	if _, _, err := tiledelivery.ReadScalar(path, digestHex); err != nil {
		return tiledelivery.ScalarAsset{}, fmt.Errorf("existing scalar tile does not match the current proof: %w", err)
	}
	return tiledelivery.ScalarAsset{
		ID: id, ReleaseID: releaseID, Period: period, Path: path, Bytes: uint64(len(encoded)), SHA256: digestHex,
		MediaType: tiledelivery.ScalarMediaType, DataTexels: stats.DataTexels, NoDataTexels: stats.NoDataTexels,
	}, nil
}

func selectPeriod(manifest delivery.Manifest, requested string) (delivery.Period, error) {
	for _, day := range manifest.Forecast.Days {
		if requested == "" || day.ID == requested {
			return day.Period, nil
		}
	}
	return delivery.Period{}, fmt.Errorf("forecast period %q is not in the current release", requested)
}

func (source scoreSource) niceScore(lon, lat float64) (uint8, bool, error) {
	if source.display {
		record, valid, err := source.reader.DisplayScoreAt(lon, lat)
		return record.NiceScore, valid, err
	}
	record, valid, err := source.reader.DailyAt(lon, lat)
	return record.NiceScore, valid, err
}

func coveringIDs(bounds delivery.GridBounds, zoom uint8) ([]tiledelivery.ID, error) {
	if bounds.West >= bounds.East || bounds.South >= bounds.North {
		return nil, errors.New("canary region has invalid bounds")
	}
	count := int64(uint64(1) << zoom)
	xStart := int64(math.Floor((bounds.West + 180) / 360 * float64(count)))
	xEnd := int64(math.Ceil((bounds.East+180)/360*float64(count))) - 1
	mercatorY := func(latitude float64) float64 {
		radians := latitude * math.Pi / 180
		return 0.5 - math.Asinh(math.Tan(radians))/(2*math.Pi)
	}
	yStart := max(0, int64(math.Floor(mercatorY(bounds.North)*float64(count))))
	yEnd := min(count-1, int64(math.Ceil(mercatorY(bounds.South)*float64(count)))-1)
	ids := make([]tiledelivery.ID, 0, int(yEnd-yStart+1)*int(xEnd-xStart+1))
	for y := yStart; y <= yEnd; y++ {
		for x := xStart; x <= xEnd; x++ {
			id, err := tiledelivery.NewID("europe", zoom, x, y)
			if err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func compressedSize(encoded []byte) (uint64, error) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(encoded); err != nil {
		return 0, err
	}
	if err := writer.Close(); err != nil {
		return 0, err
	}
	return uint64(compressed.Len()), nil
}

func buildProbes(source scoreSource, assets map[string]canaryTile, zoom uint8) ([]canaryProbe, error) {
	candidates := []struct {
		name     string
		lon, lat float64
	}{
		{"Madrid", -3.7038, 40.4168}, {"Amsterdam", 4.9041, 52.3676}, {"Athens", 23.7275, 37.9838}, {"London", -0.1276, 51.5072}, {"Oslo", 10.7522, 59.9139},
	}
	probes := make([]canaryProbe, 0, len(candidates))
	for _, candidate := range candidates {
		id, row, col, err := tiledelivery.At("europe", zoom, candidate.lon, candidate.lat)
		if err != nil {
			return nil, err
		}
		asset, ok := assets[tileKey(id)]
		if !ok {
			continue
		}
		encoded, err := os.ReadFile(asset.path)
		if err != nil {
			return nil, err
		}
		offset := (row+tiledelivery.ScalarGutter)*tiledelivery.ScalarTextureSize + col + tiledelivery.ScalarGutter
		count := math.Exp2(float64(zoom)) * tiledelivery.TileSize
		globalX := float64(id.X*tiledelivery.TileSize+uint32(col)) + .5
		globalY := float64(id.Y*tiledelivery.TileSize+uint32(row)) + .5
		lon := globalX/count*360 - 180
		lat := math.Atan(math.Sinh(math.Pi*(1-2*globalY/count))) * 180 / math.Pi
		score, valid, err := source.niceScore(lon, lat)
		if err != nil {
			return nil, err
		}
		if !valid {
			continue
		}
		if encoded[offset] != score {
			return nil, fmt.Errorf("probe %s scalar tile score %d does not match point score %d", candidate.name, encoded[offset], score)
		}
		probes = append(probes, canaryProbe{Name: candidate.name, URL: asset.URL, Offset: offset, Score: score})
	}
	return probes, nil
}

func tileKey(id tiledelivery.ID) string { return fmt.Sprintf("%d/%d/%d", id.Zoom, id.X, id.Y) }

func writeIndex(path string, index canaryIndex) error {
	encoded, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	digest := sha256.Sum256(encoded)
	temporary := path + "." + hex.EncodeToString(digest[:8]) + ".tmp"
	if err := os.WriteFile(temporary, encoded, 0o644); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	return nil
}
