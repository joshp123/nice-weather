package destinations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"nice-weather-finder/internal/forecasttiles"
	"path/filepath"
	"testing"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/forecastrange"
	"nice-weather-finder/internal/forecastruns"
	"nice-weather-finder/internal/tiledelivery"
)

func TestWorldwideRangeMustMatchTheExactCurrentIndexBytes(t *testing.T) {
	payload := []byte(`{"version":1,"releaseId":"forecast-test"}`)
	digest := sha256.Sum256(payload)
	index := forecastruns.Index{
		IndexID: "forecast-test",
		Range: forecastruns.RangeReference{
			RunUTC: "2026-07-22T00:00:00Z", CatalogBytes: uint64(len(payload)), CatalogSHA256: hex.EncodeToString(digest[:]),
		},
	}
	catalog := forecastrange.Catalog{ReleaseID: index.IndexID, Dates: []string{"2026-07-22"}}
	catalog.Source.RunUTC = index.Range.RunUTC
	if err := validateWorldwideRange(index, catalog, payload, catalog.Dates); err != nil {
		t.Fatalf("matching catalogue rejected: %v", err)
	}
	if err := validateWorldwideRange(index, catalog, append([]byte(nil), payload[:len(payload)-1]...), catalog.Dates); err == nil {
		t.Fatal("catalogue bytes not bound to the current index were accepted")
	}
	wrongDates := []string{"2026-07-23"}
	if err := validateWorldwideRange(index, catalog, payload, wrongDates); err == nil {
		t.Fatal("catalogue dates not bound to the current index were accepted")
	}
}

func TestWorldwideLookupReadsTheExactCanonicalRangeRecord(t *testing.T) {
	const longitude, latitude = -4.42034, 36.72016
	id, row, column, err := tiledelivery.At(forecasttiles.TierGlobal, forecastrange.MaxZoom, longitude, latitude)
	if err != nil {
		t.Fatal(err)
	}
	globalRow := id.Y*tiledelivery.TileSize + uint32(row)
	globalColumn := id.X*tiledelivery.TileSize + uint32(column)
	blockRow := globalRow / uint32(forecastrange.BlockRows)
	blockColumn := globalColumn / uint32(forecastrange.BlockColumns)
	identity, err := forecastrange.NewBlockIdentity("forecast-test", blockRow, blockColumn)
	if err != nil {
		t.Fatal(err)
	}
	nice := rangeRecord(t, 91)
	cycling := rangeRecord(t, 84)
	cycling.Thermal, cycling.Dryness, cycling.Sun = nice.Thermal, nice.Dryness, nice.Sun
	var niceDays, cyclingDays [delivery.ForecastPointDayCount]uint8
	for index := range niceDays {
		niceDays[index], cyclingDays[index] = 91, 84
	}
	records := make([]delivery.RangePointRecord, int(forecastrange.BlockRows)*int(forecastrange.BlockColumns))
	recordIndex := (globalRow%uint32(forecastrange.BlockRows))*uint32(forecastrange.BlockColumns) + globalColumn%uint32(forecastrange.BlockColumns)
	records[recordIndex] = delivery.RangePointRecord{Nice: nice, Cycling: cycling, NiceDays: niceDays, CyclingDays: cyclingDays}
	root := t.TempDir()
	asset, err := forecastrange.WriteBlock(root, identity, records)
	if err != nil {
		t.Fatal(err)
	}
	catalog := forecastrange.Catalog{ReleaseID: identity.ReleaseID, PointBlocks: []forecastrange.PointBlock{{
		Row: blockRow, Column: blockColumn, Path: forecastrange.BlockPath(blockRow, blockColumn),
		Bytes: asset.Bytes, SHA256: asset.SHA256, PayloadSHA256: asset.PayloadSHA256, DataRecords: asset.DataRecords, NoDataRecords: asset.NoDataRecords,
	}}}
	lookup := newWorldwideLookup(context.Background(), root, catalog)
	for profile, want := range map[string]delivery.RangeRecord{delivery.ProfileNice: nice, delivery.ProfileCycling: cycling} {
		got, valid, err := lookup.lookup(profile, longitude, latitude)
		if err != nil || !valid || got != want {
			t.Fatalf("%s lookup = %+v, valid=%t, err=%v; want %+v", profile, got, valid, err, want)
		}
	}
	if len(lookup.blocks) != 1 {
		t.Fatalf("lookup reopened the same immutable block: %d cached", len(lookup.blocks))
	}
	if _, _, err := lookup.lookup("unknown", longitude, latitude); err == nil {
		t.Fatal("unknown destination profile was accepted")
	}
	if got := filepath.Clean(asset.Path); got != filepath.Join(root, filepath.FromSlash(forecastrange.BlockPath(blockRow, blockColumn))) {
		t.Fatalf("block path = %q", got)
	}
}

func TestWorldwideLookupTreatsOmittedBlocksAsUnavailable(t *testing.T) {
	catalog := forecastrange.Catalog{ReleaseID: "forecast-test", PointBlocks: make([]forecastrange.PointBlock, 0)}
	id, row, column, err := tiledelivery.At(forecasttiles.TierGlobal, forecastrange.MaxZoom, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	key := [2]uint32{
		(id.Y*tiledelivery.TileSize + uint32(row)) / uint32(forecastrange.BlockRows),
		(id.X*tiledelivery.TileSize + uint32(column)) / uint32(forecastrange.BlockColumns),
	}
	catalog.PointBlocks = append(catalog.PointBlocks, forecastrange.PointBlock{Row: key[0], Column: key[1], Omitted: true})
	lookup := newWorldwideLookup(context.Background(), t.TempDir(), catalog)
	if _, valid, err := lookup.lookup(delivery.ProfileNice, 0, 0); err != nil || valid {
		t.Fatalf("omitted block = valid %t, err %v", valid, err)
	}
}
