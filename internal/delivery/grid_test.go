package delivery

import (
	"testing"

	"nice-weather-finder/internal/weather"
)

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
	encoded := make([]byte, dailyWeatherRecordBytes)
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

func TestDailyRecordRejectsLossyOrImpossibleEvidence(t *testing.T) {
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
			if err := validateRecord(input); err == nil {
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
