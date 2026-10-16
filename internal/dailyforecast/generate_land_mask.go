//go:build ignore

// Written by AI on 2026.10.16 by model GPT-6.1 Sol.
// Run from this directory with go run generate_land_mask.go; requires GDAL.
// Natural Earth 5.1.1, all-touched 0.25-degree cells, including both dateline edges.
package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	ResolutionDegrees = 0.25
	Width             = 1440
	Height            = 721
	landURL           = "https://naciscdn.org/naturalearth/5.1.1/10m/physical/ne_10m_land.zip"
)

func main() {
	scratch, err := os.MkdirTemp("", "niceweather-land-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(scratch)
	cells, err := landCells(context.Background(), scratch)
	if err != nil {
		panic(err)
	}
	mask := image.NewGray(image.Rect(0, 0, Width, Height))
	for _, cell := range cells {
		mask.Pix[cell] = 255
	}
	file, err := os.Create("land-mask.png")
	if err != nil {
		panic(err)
	}
	if err = png.Encode(file, mask); err != nil {
		panic(err)
	}
	if err = file.Close(); err != nil {
		panic(err)
	}
	fmt.Printf("land-mask.png: %d land cells; decoded SHA-256 %x\n", len(cells), sha256.Sum256(mask.Pix))
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func landCells(ctx context.Context, scratch string) ([]int, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, landURL, nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Natural Earth land download: HTTP %d", response.StatusCode)
	}
	archive, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	reader, err := zip.NewReader(strings.NewReader(string(archive)), int64(len(archive)))
	if err != nil {
		return nil, err
	}
	for _, member := range reader.File {
		extension := filepath.Ext(member.Name)
		if extension != ".shp" && extension != ".shx" && extension != ".dbf" && extension != ".prj" {
			continue
		}
		source, openErr := member.Open()
		if openErr != nil {
			return nil, openErr
		}
		target, createErr := os.Create(filepath.Join(scratch, filepath.Base(member.Name)))
		if createErr != nil {
			source.Close()
			return nil, createErr
		}
		_, copyErr := io.Copy(target, source)
		source.Close()
		closeErr := target.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	half := ResolutionDegrees / 2
	raster := filepath.Join(scratch, "land.bin")
	_, err = runCommand(ctx, "gdal_rasterize", "-q", "-at", "-burn", "1", "-init", "0", "-ot", "Byte", "-of", "ENVI", "-te", number(-180-half), number(-90-half), number(180-half), number(90+half), "-ts", strconv.Itoa(Width), strconv.Itoa(Height), filepath.Join(scratch, "ne_10m_land.shp"), raster)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(raster)
	if err != nil {
		return nil, err
	}
	if len(data) != Width*Height {
		return nil, fmt.Errorf("land mask has %d cells, want %d", len(data), Width*Height)
	}
	wrappingRaster := filepath.Join(scratch, "land-dateline.bin")
	_, err = runCommand(ctx, "gdal_rasterize", "-q", "-at", "-burn", "1", "-init", "0", "-ot", "Byte", "-of", "ENVI", "-te", number(180-half), number(-90-half), number(180+half), number(90+half), "-ts", "1", strconv.Itoa(Height), filepath.Join(scratch, "ne_10m_land.shp"), wrappingRaster)
	if err != nil {
		return nil, err
	}
	wrappingData, err := os.ReadFile(wrappingRaster)
	if err != nil {
		return nil, err
	}
	if len(wrappingData) != Height {
		return nil, fmt.Errorf("date line land mask has %d rows, want %d", len(wrappingData), Height)
	}
	for row, value := range wrappingData {
		data[row*Width] |= value
	}
	var cells []int
	for cell, value := range data {
		if value != 0 {
			cells = append(cells, cell)
		}
	}
	return cells, nil
}
func number(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }
