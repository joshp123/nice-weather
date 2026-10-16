package clearsky

import (
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"io"
	"math"
	"os"
	"strconv"
	"testing"
	"time"
)

type referenceHour struct {
	Name                                           string
	Latitude, Longitude, Altitude                  float64
	Monthly                                        [12]float64
	Time                                           string
	Direct, Horizontal, Normal, Daylight, Sunshine float64
}

func TestMeasuredSunshineHours(t *testing.T) {
	content, err := os.ReadFile("testdata/research-hours.json")
	if err != nil {
		t.Fatal(err)
	}
	var references []referenceHour
	if err = json.Unmarshal(content, &references); err != nil {
		t.Fatal(err)
	}
	maxHorizontal, maxNormal, maxDaylight, maxSunshine := 0.0, 0.0, 0.0, 0.0
	for _, ref := range references {
		instant, err := time.Parse(time.RFC3339, ref.Time)
		if err != nil {
			t.Fatal(err)
		}
		model := NewWithMonthlyTurbidity(ref.Latitude, ref.Longitude, ref.Altitude, ref.Monthly)
		horizontal, normal, daylight := model.HourEnding(instant)
		sunshine := SunshineHours(ref.Direct, horizontal, normal, daylight)
		maxHorizontal = math.Max(maxHorizontal, math.Abs(horizontal-ref.Horizontal))
		maxNormal = math.Max(maxNormal, math.Abs(normal-ref.Normal))
		maxDaylight = math.Max(maxDaylight, math.Abs(daylight-ref.Daylight))
		maxSunshine = math.Max(maxSunshine, math.Abs(sunshine-ref.Sunshine))
	}
	t.Logf("%d retained research hours: max errors horizontal %.9g W/m2, normal %.9g W/m2, daylight %.9g hours, sunshine %.9g hours", len(references), maxHorizontal, maxNormal, maxDaylight, maxSunshine)
	if maxHorizontal > 1e-5 || maxNormal > 1e-5 || maxDaylight > 1e-10 || maxSunshine > 1e-7 {
		t.Fatal("Go solar calculation differs from measured pvlib research")
	}
}

// The full retained hourly arrays are a research input, rather than a duplicate
// source archive in Git. Set NWF_CLEARSKY_REFERENCE to its converted binary file.
func TestFullResearchReference(t *testing.T) {
	referencePath := os.Getenv("NWF_CLEARSKY_REFERENCE")
	if referencePath == "" {
		t.Skip("full research arrays not supplied")
	}
	metadata, err := os.ReadFile(referencePath + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var places []struct {
		Name                          string
		Latitude, Longitude, Altitude float64
		Monthly                       [12]float64
		Count                         int
		Timezone                      string
	}
	if err = json.Unmarshal(metadata, &places); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(referencePath + ".bin")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	dailyReader, err := os.Open(referencePath + ".daily.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer dailyReader.Close()
	dailyCSV, err := csv.NewReader(dailyReader).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	expectedDays := make(map[string]float64)
	for _, row := range dailyCSV[1:] {
		value, err := strconv.ParseFloat(row[11], 64)
		if err != nil {
			t.Fatal(err)
		}
		expectedDays[row[0]+"/"+row[1]] = value
	}
	cachedHours := make(map[int64]Hour)
	for _, place := range places {
		model := NewWithMonthlyTurbidity(place.Latitude, place.Longitude, place.Altitude, place.Monthly)
		location, err := time.LoadLocation(place.Timezone)
		if err != nil {
			t.Fatal(err)
		}
		maxHorizontal, maxNormal, maxDaylight, maxSunshine := 0.0, 0.0, 0.0, 0.0
		type dailyTotals struct{ sun, up float64 }
		days := make(map[string]dailyTotals)
		for i := 0; i < place.Count; i++ {
			var timestamp int64
			var values [5]float64
			if err = binary.Read(reader, binary.LittleEndian, &timestamp); err != nil {
				t.Fatal(err)
			}
			if err = binary.Read(reader, binary.LittleEndian, &values); err != nil {
				t.Fatal(err)
			}
			instant := time.Unix(timestamp, 0).UTC()
			hour, found := cachedHours[timestamp]
			if !found {
				hour = PrepareHour(instant)
				cachedHours[timestamp] = hour
			}
			horizontal, normal, daylight := model.Hour(hour)
			sunshine := SunshineHours(values[0], horizontal, normal, daylight)
			maxHorizontal = math.Max(maxHorizontal, math.Abs(horizontal-values[1]))
			maxNormal = math.Max(maxNormal, math.Abs(normal-values[2]))
			maxDaylight = math.Max(maxDaylight, math.Abs(daylight-values[3]))
			maxSunshine = math.Max(maxSunshine, math.Abs(sunshine-values[4]))
			local := instant.Add(-time.Hour).In(location)
			if local.Hour() >= 10 && local.Hour() < 20 {
				key := local.Format("2006-01-02")
				d := days[key]
				d.sun += sunshine
				d.up += daylight
				days[key] = d
			}
		}
		maxFraction := 0.0
		matchedDays := 0
		for date, d := range days {
			reference, found := expectedDays[place.Name+"/"+date]
			if !found {
				continue
			}
			if d.up > 0 {
				maxFraction = math.Max(maxFraction, math.Abs(d.sun/d.up-reference))
				matchedDays++
			}
		}
		if matchedDays < 7304 {
			t.Fatalf("%s matched only %d days", place.Name, matchedDays)
		}
		t.Logf("%s: %d hours, %d local days, max beam errors %.9g / %.9g W/m2, daylight %.9g h, sunshine %.9g h, daily sun share %.9g", place.Name, place.Count, matchedDays, maxHorizontal, maxNormal, maxDaylight, maxSunshine, maxFraction)
		if maxHorizontal > 1e-5 || maxNormal > 1e-5 || maxDaylight > 1e-10 || maxSunshine > 1e-7 || maxFraction > 1e-7 {
			t.Errorf("%s exceeds measured reference tolerances", place.Name)
		}
	}
	extra := make([]byte, 1)
	if _, err = reader.Read(extra); err != io.EOF {
		t.Fatal("unconsumed reference records")
	}
}
