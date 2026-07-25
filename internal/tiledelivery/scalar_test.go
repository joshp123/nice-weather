package tiledelivery

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"sync"
	"testing"
)

func TestScalarTileHasExactBrowserContractAndDeterministicBytes(t *testing.T) {
	id, _ := NewID("global", 3, 4, 3)
	sampler := func(point ScalarSample) (uint8, bool, error) {
		return uint8((point.GlobalX + point.GlobalY) % 101), true, nil
	}
	first, firstStats, err := BuildScalarTile(id, sampler)
	if err != nil {
		t.Fatal(err)
	}
	second, secondStats, err := BuildScalarTile(id, sampler)
	if err != nil {
		t.Fatal(err)
	}
	if ScalarTextureSize != 258 || len(first) != ScalarTileBytes {
		t.Fatalf("decoded bytes = %d; want %d", len(first), ScalarTileBytes)
	}
	if !bytes.Equal(first, second) || firstStats != secondStats {
		t.Fatal("same evaluated samples produced different tile bytes")
	}
	if firstStats.DataTexels != ScalarTileBytes || firstStats.NoDataTexels != 0 || firstStats.DecodedBytes != ScalarTileBytes {
		t.Fatalf("stats = %+v", firstStats)
	}

	var compressed bytes.Buffer
	zipper, err := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zipper.Write(first); err != nil {
		t.Fatal(err)
	}
	if err := zipper.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("scalar texture: decoded=%d bytes, gzip=%d bytes", len(first), compressed.Len())
	if compressed.Len() >= len(first) {
		t.Fatal("ordinary HTTP gzip did not reduce deterministic canary tile")
	}
}

func TestScalarTileGuttersMatchAdjacentInterior(t *testing.T) {
	sampler := func(point ScalarSample) (uint8, bool, error) {
		return uint8((point.GlobalX*7 + point.GlobalY*11) % 101), true, nil
	}
	centre, _ := NewID("global", 4, 8, 7)
	east, _ := NewID("global", 4, 9, 7)
	south, _ := NewID("global", 4, 8, 8)
	centreBytes, _, _ := BuildScalarTile(centre, sampler)
	eastBytes, _, _ := BuildScalarTile(east, sampler)
	southBytes, _, _ := BuildScalarTile(south, sampler)

	for row := ScalarGutter; row < ScalarGutter+TileSize; row++ {
		if got, want := scalarAt(centreBytes, row, ScalarTextureSize-1), scalarAt(eastBytes, row, ScalarGutter); got != want {
			t.Fatalf("east gutter row %d = %d; adjacent interior = %d", row, got, want)
		}
		if got, want := scalarAt(eastBytes, row, 0), scalarAt(centreBytes, row, ScalarGutter+TileSize-1); got != want {
			t.Fatalf("west gutter row %d = %d; adjacent interior = %d", row, got, want)
		}
	}
	for column := ScalarGutter; column < ScalarGutter+TileSize; column++ {
		if got, want := scalarAt(centreBytes, ScalarTextureSize-1, column), scalarAt(southBytes, ScalarGutter, column); got != want {
			t.Fatalf("south gutter column %d = %d; adjacent interior = %d", column, got, want)
		}
		if got, want := scalarAt(southBytes, 0, column), scalarAt(centreBytes, ScalarGutter+TileSize-1, column); got != want {
			t.Fatalf("north gutter column %d = %d; adjacent interior = %d", column, got, want)
		}
	}
}

func TestScalarTileWrapsAntimeridianWithoutWrappingPoles(t *testing.T) {
	const zoom = uint8(2)
	west, _ := NewID("global", zoom, 0, 0)
	east, _ := NewID("global", zoom, 3, 0)
	sampler := func(point ScalarSample) (uint8, bool, error) {
		return uint8(point.GlobalX % 101), true, nil
	}
	westBytes, westStats, err := BuildScalarTile(west, sampler)
	if err != nil {
		t.Fatal(err)
	}
	eastBytes, _, err := BuildScalarTile(east, sampler)
	if err != nil {
		t.Fatal(err)
	}
	for row := ScalarGutter; row < ScalarGutter+TileSize; row++ {
		if got, want := scalarAt(westBytes, row, 0), scalarAt(eastBytes, row, ScalarGutter+TileSize-1); got != want {
			t.Fatalf("west antimeridian gutter row %d = %d; east interior = %d", row, got, want)
		}
	}
	if westStats.NoDataTexels != ScalarTextureSize {
		t.Fatalf("north outer gutter no-data = %d; want %d", westStats.NoDataTexels, ScalarTextureSize)
	}
	for column := 0; column < ScalarTextureSize; column++ {
		if got := scalarAt(westBytes, 0, column); got != ScalarNoData {
			t.Fatalf("north outer gutter column %d = %d; want no-data", column, got)
		}
	}
}

func TestScalarTileSamplesExactXYZTexelCentres(t *testing.T) {
	id, _ := NewID("global", 1, 0, 0)
	foundNorthWest, foundWrappedWest := false, false
	_, _, err := BuildScalarTile(id, func(point ScalarSample) (uint8, bool, error) {
		if point.GlobalX == 0 && point.GlobalY == 0 {
			foundNorthWest = true
			if point.Longitude != -179.6484375 || math.Abs(point.Latitude-85.02070774312593) > 1e-12 {
				t.Fatalf("north-west texel centre = %.15f,%.15f", point.Longitude, point.Latitude)
			}
		}
		if point.GlobalX == 511 && point.GlobalY == 0 {
			foundWrappedWest = true
			if point.Longitude != 179.6484375 || math.Abs(point.Latitude-85.02070774312593) > 1e-12 {
				t.Fatalf("wrapped west gutter centre = %.15f,%.15f", point.Longitude, point.Latitude)
			}
		}
		return 50, true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !foundNorthWest || !foundWrappedWest {
		t.Fatalf("sampled north-west=%t wrapped-west=%t", foundNorthWest, foundWrappedWest)
	}
}

func TestScalarTilePreservesCoastAndNoData(t *testing.T) {
	id, _ := NewID("europe-detail", 7, 62, 49)
	sampler := func(point ScalarSample) (uint8, bool, error) {
		// A deterministic half-plane stands in for the canonical land mask.
		if point.GlobalX < int64(id.X)*TileSize+128 {
			return 88, true, nil
		}
		return 0, false, nil
	}
	encoded, stats, err := BuildScalarTile(id, sampler)
	if err != nil {
		t.Fatal(err)
	}
	if scalarAt(encoded, 100, ScalarGutter+127) != 88 || scalarAt(encoded, 100, ScalarGutter+128) != ScalarNoData {
		t.Fatal("coast boundary was filled or shifted")
	}
	if stats.DataTexels+stats.NoDataTexels != ScalarTileBytes || stats.DataTexels == 0 || stats.NoDataTexels == 0 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestScalarTileRejectsInvalidSamplesAndSamplerErrors(t *testing.T) {
	id, _ := NewID("global", 2, 1, 1)
	if _, _, err := BuildScalarTile(id, func(ScalarSample) (uint8, bool, error) { return ForecastScalarFallback, true, nil }); err == nil {
		t.Fatal("generic scalar accepted forecast fallback sentinel")
	}
	encoded, stats, err := BuildForecastScalarTile(id, func(ScalarSample) (uint8, bool, error) { return ForecastScalarFallback, true, nil })
	if err != nil || encoded[ScalarGutter*ScalarTextureSize+ScalarGutter] != ForecastScalarFallback || stats.DataTexels != ScalarTileBytes {
		t.Fatalf("fallback sentinel = %d stats=%+v err=%v", encoded[ScalarGutter*ScalarTextureSize+ScalarGutter], stats, err)
	}
	if _, _, err := BuildScalarTile(id, func(ScalarSample) (uint8, bool, error) { return 101, true, nil }); err == nil {
		t.Fatal("accepted score above canonical range")
	}
	want := errors.New("source failed")
	if _, _, err := BuildScalarTile(id, func(ScalarSample) (uint8, bool, error) { return 0, false, want }); !errors.Is(err, want) {
		t.Fatalf("sampler error = %v; want %v", err, want)
	}
}

func TestScalarPublicationAndReadAreImmutableAndFailClosed(t *testing.T) {
	root := t.TempDir()
	writer, _ := NewWriter(root, "release-canary")
	id, _ := NewID("global", 3, 4, 3)
	sampler := func(ScalarSample) (uint8, bool, error) { return 73, true, nil }
	asset, err := writer.WriteScalar(id, "day-01", sampler)
	if err != nil {
		t.Fatal(err)
	}
	if asset.Omitted || asset.Bytes != ScalarTileBytes || asset.MediaType != ScalarMediaType || asset.DataTexels != ScalarTileBytes || asset.NoDataTexels != 0 {
		t.Fatalf("asset = %+v", asset)
	}
	decoded, stats, err := ReadScalar(asset.Path, asset.SHA256)
	if err != nil || len(decoded) != ScalarTileBytes || stats.DataTexels != ScalarTileBytes {
		t.Fatalf("read = %d bytes, %+v, %v", len(decoded), stats, err)
	}
	if _, err := writer.WriteScalar(id, "day-01", sampler); err == nil {
		t.Fatal("overwrote immutable scalar tile")
	}

	truncated := asset.Path + ".truncated"
	if err := os.WriteFile(truncated, decoded[:len(decoded)-1], 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadScalar(truncated, asset.SHA256); err == nil {
		t.Fatal("accepted truncated scalar tile")
	}
	oversized := asset.Path + ".oversized"
	oversizedFile, err := os.Create(oversized)
	if err != nil {
		t.Fatal(err)
	}
	if err := oversizedFile.Truncate(int64(ScalarTileBytes) + 1<<30); err != nil {
		_ = oversizedFile.Close()
		t.Fatal(err)
	}
	if err := oversizedFile.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadScalar(oversized, asset.SHA256); err == nil {
		t.Fatal("accepted oversized scalar tile")
	}
	corrupt := append([]byte(nil), decoded...)
	corrupt[123] = 72
	corruptPath := asset.Path + ".corrupt"
	if err := os.WriteFile(corruptPath, corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadScalar(corruptPath, asset.SHA256); err == nil {
		t.Fatal("accepted checksum-mismatched scalar tile")
	}
	invalid := append([]byte(nil), decoded...)
	invalid[123] = 200
	invalidPath := asset.Path + ".invalid"
	if err := os.WriteFile(invalidPath, invalid, 0o644); err != nil {
		t.Fatal(err)
	}
	invalidDigest := sha256Hex(invalid)
	if _, _, err := ReadScalar(invalidPath, invalidDigest); err == nil {
		t.Fatal("accepted invalid scalar value with a matching digest")
	}
}

func TestAllNoDataScalarTileIsOmitted(t *testing.T) {
	root := t.TempDir()
	writer, _ := NewWriter(root, "release-canary")
	id, _ := NewID("global", 2, 1, 1)
	asset, err := writer.WriteScalar(id, "day-01", func(ScalarSample) (uint8, bool, error) { return 0, false, nil })
	if err != nil {
		t.Fatal(err)
	}
	if !asset.Omitted || asset.Path != "" || asset.NoDataTexels != ScalarTileBytes {
		t.Fatalf("omitted asset = %+v", asset)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("all-no-data tile wrote %d entries", len(entries))
	}
}

func TestConcurrentScalarPublicationHasOneWinner(t *testing.T) {
	writer, _ := NewWriter(t.TempDir(), "release-canary")
	id, _ := NewID("global", 2, 1, 1)
	const contenders = 8
	results := make(chan error, contenders)
	var wait sync.WaitGroup
	for score := uint8(50); score < 50+contenders; score++ {
		wait.Add(1)
		go func(value uint8) {
			defer wait.Done()
			_, err := writer.WriteScalar(id, "day-01", func(ScalarSample) (uint8, bool, error) { return value, true, nil })
			results <- err
		}(score)
	}
	wait.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent immutable writes had %d winners; want 1", winners)
	}
}

func scalarAt(encoded []byte, row, column int) byte {
	return encoded[row*ScalarTextureSize+column]
}

func sha256Hex(encoded []byte) string {
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
