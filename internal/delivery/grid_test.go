package delivery

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"nice-weather-finder/internal/weather"
)

func TestGridRoundTripAndPointRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "day.nwg")
	spec := GridSpec{Bounds: GridBounds{West: 0, East: 4, South: 50, North: 52}, Rows: 2, Cols: 4}
	records := []DailyRecord{
		{ComfortableTemperatureDeciC: 210, MinTemperatureDeciC: 170, MaxTemperatureDeciC: 250, DewPointDeciC: 80, RainDeciMM: 12, WindDeciKMH: 113, GustDeciKMH: 205, SolarCentiMJm2: 1830, WetTimePermille: 125, CloudPercent: 20, HumidityPercent: 45, Valid: true, HasDewPoint: true, HasGust: true, HasSolar: true, HasCloud: true, HasHumidity: true, HasWetTime: true},
		{ComfortableTemperatureDeciC: -15, MinTemperatureDeciC: -30, MaxTemperatureDeciC: 0, DewPointDeciC: -30, CloudPercent: 100, Valid: true, HasDewPoint: true, HasCloud: true},
		{},
		record(290),
		record(101),
		record(102),
		record(103),
		record(104),
	}
	written, err := WriteGrid(path, spec, records)
	if err != nil {
		t.Fatal(err)
	}
	if len(written.SHA256) != 64 || written.Rows != 2 || written.Cols != 4 || written.RecordSize != gridRecordSize {
		t.Fatalf("unexpected metadata: %+v", written)
	}
	reader, err := OpenGrid(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if reader.Metadata() != written {
		t.Fatalf("read metadata %+v; want %+v", reader.Metadata(), written)
	}

	checks := []struct {
		lon, lat float64
		want     DailyRecord
		valid    bool
	}{
		{0, 52, records[0], true},         // north-west outer edge
		{3.9, 52, records[3], true},       // north-east displayed edge cell
		{0, 50, records[4], true},         // south-west outer edge
		{3.9, 50, records[7], true},       // south-east displayed edge cell
		{1, 51, records[1], true},         // longitude tie chooses east; projected row matches the overlay texel
		{2.2, 51.7, DailyRecord{}, false}, // explicit no-data cell
		{-0.01, 51, DailyRecord{}, false},
		{4.01, 51, DailyRecord{}, false},
		{1, 52.01, DailyRecord{}, false},
		{1, 49.99, DailyRecord{}, false},
		{math.NaN(), 51, DailyRecord{}, false},
	}
	for _, check := range checks {
		got, valid, err := reader.RecordAt(check.lon, check.lat)
		if err != nil {
			t.Fatalf("RecordAt(%v, %v): %v", check.lon, check.lat, err)
		}
		if valid != check.valid || got != check.want {
			t.Errorf("RecordAt(%v, %v) = (%+v, %v); want (%+v, %v)", check.lon, check.lat, got, valid, check.want, check.valid)
		}
	}
}

func TestWeatherRecordConversionPreservesCanonicalEvidence(t *testing.T) {
	metrics := weather.DailyMetrics{
		ComfortableTempC: 22.34, MinTempC: 17.26, MaxTempC: 26.75,
		RainMM: 999.94, WetTimeFraction: weather.OptionalFloat{Value: .4564, Available: true},
		SolarEnergyMJm2:   weather.OptionalFloat{Value: 18.234, Available: true},
		CloudCoverPercent: weather.OptionalFloat{Value: 23.4, Available: true},
		WindSpeedKPH:      12.34, GustKPH: weather.OptionalFloat{Value: 19.96, Available: true},
		DewPointC:               weather.OptionalFloat{Value: 11.24, Available: true},
		RelativeHumidityPercent: weather.OptionalFloat{Value: 61.6, Available: true},
		NativeResolutionKM:      7, Valid: true,
	}
	record, err := RecordFromWeather(metrics)
	if err != nil {
		t.Fatal(err)
	}
	if record.RainDeciMM != 9999 || record.WetTimePermille != 456 || record.SolarCentiMJm2 != 1823 || record.HumidityPercent != 62 {
		t.Fatalf("unexpected quantization: %+v", record)
	}
	roundTrip, err := record.WeatherMetrics(7)
	if err != nil {
		t.Fatal(err)
	}
	if !roundTrip.Valid || !roundTrip.WetTimeFraction.Available || roundTrip.WetTimeFraction.Value != .456 || !roundTrip.GustKPH.Available || !roundTrip.SolarEnergyMJm2.Available || roundTrip.RainMM != 999.9 {
		t.Fatalf("lost canonical evidence: %+v", roundTrip)
	}

	metrics.GustKPH = weather.OptionalFloat{Value: 1, Available: false}
	if _, err := RecordFromWeather(metrics); err == nil {
		t.Fatal("hidden optional evidence was accepted")
	}
	if _, err := quantizeUint16(70000, 10, "overflow"); err == nil {
		t.Fatal("quantization overflow was accepted")
	}
	metrics.GustKPH = weather.OptionalFloat{Value: 20, Available: true}
	metrics.WetTimeFraction = weather.OptionalFloat{}
	record, err = RecordFromWeather(metrics)
	if err != nil {
		t.Fatal(err)
	}
	if record.HasWetTime || record.WetTimePermille != 0 {
		t.Fatal("missing wet-time evidence was invented")
	}
	roundTrip, err = record.WeatherMetrics(7)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.WetTimeFraction.Available || roundTrip.WetTimeFraction.Value != 0 {
		t.Fatal("missing wet-time evidence did not round-trip")
	}
	metrics.WetTimeFraction = weather.OptionalFloat{Value: .2}
	if _, err := RecordFromWeather(metrics); err == nil {
		t.Fatal("hidden wet-time value was accepted")
	}
}

func TestDecodeRecordRejectsUnknownFlagsAndReservedBytes(t *testing.T) {
	encoded := make([]byte, gridRecordSize)
	encodeRecord(encoded, record(200))
	encoded[20] |= 0x80
	if _, err := decodeRecord(encoded); err == nil {
		t.Fatal("unknown record flag was accepted")
	}
	encodeRecord(encoded, record(200))
	encoded[21] = 1
	if _, err := decodeRecord(encoded); err == nil {
		t.Fatal("reserved record byte was accepted")
	}
}

func TestGridRejectsInvalidFiles(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "valid.nwg")
	_, err := WriteGrid(valid, GridSpec{Bounds: GridBounds{West: 0, East: 1, South: 0, North: 1}, Rows: 1, Cols: 1}, []DailyRecord{record(200)})
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(valid)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"truncated":       original[:100],
		"bad magic":       mutate(original, 0),
		"changed bounds":  mutate(original, 24),
		"changed payload": mutate(original, gridHeaderSize),
		"reserved header": mutate(original, 100),
		"extra bytes":     append(append([]byte(nil), original...), 0),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name+".nwg")
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := ValidateGrid(path); err == nil {
				t.Fatal("invalid grid was accepted")
			}
		})
	}
}

func TestWriteGridRejectsShapeMismatch(t *testing.T) {
	_, err := WriteGrid(filepath.Join(t.TempDir(), "bad.nwg"), GridSpec{Bounds: GridBounds{West: 0, East: 1, South: 0, North: 1}, Rows: 2, Cols: 2}, make([]DailyRecord, 3))
	if err == nil {
		t.Fatal("shape mismatch was accepted")
	}
}

func TestWriteGridRejectsLossyOrImpossibleEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.nwg")
	spec := GridSpec{Bounds: GridBounds{West: 0, East: 1, South: 0, North: 1}, Rows: 1, Cols: 1}
	cases := map[string]DailyRecord{
		"temperature range": {ComfortableTemperatureDeciC: 100, MinTemperatureDeciC: 210, MaxTemperatureDeciC: 250, Valid: true, HasCloud: true, HasHumidity: true},
		"wet time":          {ComfortableTemperatureDeciC: 200, MinTemperatureDeciC: 150, MaxTemperatureDeciC: 250, WetTimePermille: 1001, Valid: true, HasCloud: true, HasHumidity: true, HasWetTime: true},
		"hidden wet time":   {ComfortableTemperatureDeciC: 200, MinTemperatureDeciC: 150, MaxTemperatureDeciC: 250, WetTimePermille: 1, Valid: true, HasCloud: true, HasHumidity: true},
		"comfort evidence":  {ComfortableTemperatureDeciC: 200, MinTemperatureDeciC: 150, MaxTemperatureDeciC: 250, Valid: true, HasCloud: true},
		"sky evidence":      {ComfortableTemperatureDeciC: 200, MinTemperatureDeciC: 150, MaxTemperatureDeciC: 250, Valid: true, HasHumidity: true},
		"cloud range":       {ComfortableTemperatureDeciC: 200, MinTemperatureDeciC: 150, MaxTemperatureDeciC: 250, CloudPercent: 101, Valid: true, HasCloud: true, HasHumidity: true},
		"humidity range":    {ComfortableTemperatureDeciC: 200, MinTemperatureDeciC: 150, MaxTemperatureDeciC: 250, HumidityPercent: 101, Valid: true, HasCloud: true, HasHumidity: true},
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := WriteGrid(path, spec, []DailyRecord{input}); err == nil {
				t.Fatal("invalid delivery evidence was accepted")
			}
		})
	}
}

func record(temperature int16) DailyRecord {
	return DailyRecord{ComfortableTemperatureDeciC: temperature, MinTemperatureDeciC: temperature - 10, MaxTemperatureDeciC: temperature + 10, CloudPercent: 50, HumidityPercent: 60, Valid: true, HasCloud: true, HasHumidity: true}
}

func mutate(original []byte, offset int) []byte {
	result := append([]byte(nil), original...)
	result[offset] ^= 0xff
	return result
}
