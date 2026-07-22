// Package era5canary validates a bounded ERA5 hourly extraction and applies
// the unchanged canonical Nice Weather scorer. It does not publish climate
// artifacts or define a production source policy.
package era5canary

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"nice-weather-finder/internal/weather"
)

const (
	InputSchemaVersion     = 1
	ReportSchemaVersion    = 1
	PinnedSnapshotID       = "C2QSEC9FP3SJ6MT4D94G"
	MaximumInputBytes      = 8 << 20
	MaximumCells           = 11
	MaximumDays            = 900
	MaximumRequests        = 200
	MaximumResponseBytes   = 32 << 20
	MaximumTransferBytes   = 160 << 20
	MaximumDewPointExcessC = 0.01
)

type Input struct {
	SchemaVersion int       `json:"schemaVersion"`
	Source        Source    `json:"source"`
	Window        Window    `json:"window"`
	Year          int       `json:"year"`
	Retrieval     Retrieval `json:"retrieval"`
	Cells         []Cell    `json:"cells"`
	Days          []Day     `json:"days"`
}

type Source struct {
	Authority          string     `json:"authority"`
	Dataset            string     `json:"dataset"`
	Repository         string     `json:"repository"`
	SnapshotID         string     `json:"snapshotId"`
	Transport          string     `json:"transport"`
	GridDegrees        float64    `json:"gridDegrees"`
	NativeResolutionKM float64    `json:"nativeResolutionKm"`
	Variables          []Variable `json:"variables"`
}

type Variable struct {
	Name      string `json:"name"`
	Units     string `json:"units"`
	Semantics string `json:"semantics"`
}

type Window struct {
	ID                    string  `json:"id"`
	StartLocalHour        int     `json:"startLocalHour"`
	EndLocalHour          int     `json:"endLocalHour"`
	HourlyIntervalsPerDay int     `json:"hourlyIntervalsPerDay"`
	WetHourlyThresholdMM  float64 `json:"wetHourlyThresholdMm"`
}

type Retrieval struct {
	RequestCount             int   `json:"requestCount"`
	GetCount                 int   `json:"getCount"`
	HeadCount                int   `json:"headCount"`
	ResponseBodyBytes        int64 `json:"responseBodyBytes"`
	LogicalArrayReads        int   `json:"logicalArrayReads"`
	DecodedArrayBytes        int64 `json:"decodedArrayBytes"`
	MaximumResponseBodyBytes int64 `json:"maximumResponseBodyBytes"`
	SourceScratchPeakBytes   int64 `json:"sourceScratchPeakBytes"`
	SourceScratchEndBytes    int64 `json:"sourceScratchEndBytes"`
	PeakProcessRSSBytes      int64 `json:"peakProcessRssBytes"`
	WallTimeMilliseconds     int64 `json:"wallTimeMilliseconds"`
}

type Cell struct {
	ID                 string  `json:"id"`
	Scenario           string  `json:"scenario"`
	Timezone           string  `json:"timezone"`
	RequestedLatitude  float64 `json:"requestedLatitude"`
	RequestedLongitude float64 `json:"requestedLongitude"`
	GridLatitude       float64 `json:"gridLatitude"`
	GridLongitude      float64 `json:"gridLongitude"`
	LatitudeIndex      int     `json:"latitudeIndex"`
	LongitudeIndex     int     `json:"longitudeIndex"`
	Months             []int   `json:"months"`
}

type Day struct {
	CellID  string        `json:"cellId"`
	Metrics DailyEvidence `json:"metrics"`
}

type DailyEvidence struct {
	Date                           string  `json:"date"`
	Latitude                       float64 `json:"latitude"`
	Longitude                      float64 `json:"longitude"`
	ComfortableTempC               float64 `json:"comfortableTempC"`
	MinTempC                       float64 `json:"minTempC"`
	MaxTempC                       float64 `json:"maxTempC"`
	RainMM                         float64 `json:"rainMm"`
	WetTimeFraction                float64 `json:"wetTimeFraction"`
	SolarEnergyMJm2                float64 `json:"solarEnergyMJm2"`
	CloudCoverPercent              float64 `json:"cloudCoverPercent"`
	WindSpeedKPH                   float64 `json:"windSpeedKmh"`
	DewPointC                      float64 `json:"dewPointC"`
	RelativeHumidityPercent        float64 `json:"relativeHumidityPercent"`
	HumiditySaturationClampedHours int     `json:"humiditySaturationClampedHours"`
	MaximumDewPointExcessC         float64 `json:"maximumDewPointExcessC"`
}

type Report struct {
	SchemaVersion  int               `json:"schemaVersion"`
	Source         Source            `json:"source"`
	Window         Window            `json:"window"`
	Year           int               `json:"year"`
	Retrieval      Retrieval         `json:"retrieval"`
	Scope          Scope             `json:"scope"`
	ScorePolicy    string            `json:"scorePolicy"`
	Cells          []Cell            `json:"cells"`
	Scenarios      []ScenarioSummary `json:"scenarios"`
	Seam           []SeamSummary     `json:"seam"`
	Days           []ScoredDay       `json:"days"`
	HumidityRepair HumidityRepair    `json:"humidityRepair"`
}

type Scope struct {
	Proves       []string `json:"proves"`
	DoesNotProve []string `json:"doesNotProve"`
}

type HumidityRepair struct {
	SaturationClampedHours int     `json:"saturationClampedHours"`
	MaximumDewPointExcessC float64 `json:"maximumDewPointExcessC"`
	ToleranceC             float64 `json:"toleranceC"`
}

type ScoredDay struct {
	CellID        string        `json:"cellId"`
	Metrics       DailyEvidence `json:"metrics"`
	Nice          Score         `json:"nice"`
	Cycling       Score         `json:"cycling"`
	niceResult    weather.Result
	cyclingResult weather.Result
}

type Score struct {
	Overall int    `json:"overall"`
	Rating  string `json:"rating"`
	Comfort int    `json:"comfort"`
	Dry     int    `json:"dry"`
	Bright  int    `json:"bright"`
	Calm    int    `json:"calm"`
}

type ScenarioSummary struct {
	CellID             string        `json:"cellId"`
	Scenario           string        `json:"scenario"`
	Month              int           `json:"month"`
	Days               int           `json:"days"`
	NiceDaysPercent    int           `json:"niceDaysPercent"`
	AwfulDaysPercent   int           `json:"awfulDaysPercent"`
	MedianNiceScore    int           `json:"medianNiceScore"`
	MedianCyclingScore int           `json:"medianCyclingScore"`
	Physical           PhysicalRange `json:"physical"`
}

type PhysicalRange struct {
	DaytimeTemperatureC     ValueRange `json:"daytimeTemperatureC"`
	DewPointC               ValueRange `json:"dewPointC"`
	RainMM                  ValueRange `json:"rainMm"`
	WetTimeFraction         ValueRange `json:"wetTimeFraction"`
	SolarEnergyMJm2         ValueRange `json:"solarEnergyMJm2"`
	CloudCoverPercent       ValueRange `json:"cloudCoverPercent"`
	WindSpeedKPH            ValueRange `json:"windSpeedKmh"`
	RelativeHumidityPercent ValueRange `json:"relativeHumidityPercent"`
}

type ValueRange struct {
	Minimum float64 `json:"minimum"`
	Mean    float64 `json:"mean"`
	Maximum float64 `json:"maximum"`
}

type SeamSummary struct {
	Month                                   int     `json:"month"`
	CellCount                               int     `json:"cellCount"`
	BoundaryWestLongitude                   float64 `json:"boundaryWestLongitude"`
	BoundaryEastLongitude                   float64 `json:"boundaryEastLongitude"`
	MaximumBoundaryTemperatureDeltaC        float64 `json:"maximumBoundaryTemperatureDeltaC"`
	MaximumBoundaryComfortScoreDelta        int     `json:"maximumBoundaryComfortScoreDelta"`
	MaximumBoundaryOverallScoreDelta        int     `json:"maximumBoundaryOverallScoreDelta"`
	MaximumBoundaryTemperatureDate          string  `json:"maximumBoundaryTemperatureDate"`
	MaximumBoundaryComfortDate              string  `json:"maximumBoundaryComfortDate"`
	MaximumBoundaryOverallDate              string  `json:"maximumBoundaryOverallDate"`
	MaximumAdjacentTemperatureDeltaC        float64 `json:"maximumAdjacentTemperatureDeltaC"`
	MaximumAdjacentComfortScoreDelta        int     `json:"maximumAdjacentComfortScoreDelta"`
	MaximumAdjacentOverallScoreDelta        int     `json:"maximumAdjacentOverallScoreDelta"`
	MaximumAdjacentTemperatureDate          string  `json:"maximumAdjacentTemperatureDate"`
	MaximumAdjacentComfortDate              string  `json:"maximumAdjacentComfortDate"`
	MaximumAdjacentOverallDate              string  `json:"maximumAdjacentOverallDate"`
	MaximumAdjacentTemperatureWestLongitude float64 `json:"maximumAdjacentTemperatureWestLongitude"`
	MaximumAdjacentTemperatureEastLongitude float64 `json:"maximumAdjacentTemperatureEastLongitude"`
	MaximumAdjacentComfortWestLongitude     float64 `json:"maximumAdjacentComfortWestLongitude"`
	MaximumAdjacentComfortEastLongitude     float64 `json:"maximumAdjacentComfortEastLongitude"`
	MaximumAdjacentOverallWestLongitude     float64 `json:"maximumAdjacentOverallWestLongitude"`
	MaximumAdjacentOverallEastLongitude     float64 `json:"maximumAdjacentOverallEastLongitude"`
}

func Decode(input io.Reader) (Input, error) {
	if input == nil {
		return Input{}, errors.New("ERA5 canary input is unavailable")
	}
	encoded, err := io.ReadAll(io.LimitReader(input, MaximumInputBytes+1))
	if err != nil {
		return Input{}, fmt.Errorf("read ERA5 canary input: %w", err)
	}
	if len(encoded) > MaximumInputBytes {
		return Input{}, fmt.Errorf("ERA5 canary input exceeds %d bytes", MaximumInputBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var result Input
	if err := decoder.Decode(&result); err != nil {
		return Input{}, fmt.Errorf("decode ERA5 canary input: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return Input{}, errors.New("ERA5 canary input has a trailing JSON value")
	}
	return result, nil
}

func (day DailyEvidence) metrics() (weather.DailyMetrics, error) {
	if _, err := time.Parse(time.DateOnly, day.Date); err != nil || !finite(day.Latitude, day.Longitude) || day.Latitude < -90 || day.Latitude > 90 || day.Longitude < -180 || day.Longitude > 180 {
		return weather.DailyMetrics{}, errors.New("invalid ERA5 date or coordinate")
	}
	metrics := weather.DailyMetrics{
		ComfortableTempC: day.ComfortableTempC, MinTempC: day.MinTempC, MaxTempC: day.MaxTempC,
		RainMM: day.RainMM, WetTimeFraction: known(day.WetTimeFraction),
		SolarEnergyMJm2: known(day.SolarEnergyMJm2), CloudCoverPercent: known(day.CloudCoverPercent),
		WindSpeedKPH: day.WindSpeedKPH, DewPointC: known(day.DewPointC),
		RelativeHumidityPercent: known(day.RelativeHumidityPercent), NativeResolutionKM: 30, Valid: true,
	}
	if err := metrics.Validate(); err != nil {
		return weather.DailyMetrics{}, err
	}
	return metrics, nil
}

func known(value float64) weather.OptionalFloat {
	return weather.OptionalFloat{Value: value, Available: true}
}

func finite(values ...float64) bool {
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return true
}

func Evaluate(input Input) (Report, error) {
	if err := validateInput(input); err != nil {
		return Report{}, err
	}
	cells := make(map[string]Cell, len(input.Cells))
	for _, cell := range input.Cells {
		cells[cell.ID] = cell
	}

	days := append([]Day(nil), input.Days...)
	sort.Slice(days, func(i, j int) bool {
		if days[i].CellID == days[j].CellID {
			return days[i].Metrics.Date < days[j].Metrics.Date
		}
		return days[i].CellID < days[j].CellID
	})
	scored := make([]ScoredDay, 0, len(days))
	seen := make(map[string]struct{}, len(days))
	humidityRepair := HumidityRepair{ToleranceC: MaximumDewPointExcessC}
	for _, day := range days {
		cell, ok := cells[day.CellID]
		if !ok {
			return Report{}, fmt.Errorf("ERA5 day refers to unknown cell %q", day.CellID)
		}
		date, err := time.Parse(time.DateOnly, day.Metrics.Date)
		if err != nil || date.Year() != input.Year || !slices.Contains(cell.Months, int(date.Month())) {
			return Report{}, fmt.Errorf("ERA5 day %s for %s is outside the declared sample", day.Metrics.Date, day.CellID)
		}
		key := day.CellID + "/" + day.Metrics.Date
		if _, duplicate := seen[key]; duplicate {
			return Report{}, fmt.Errorf("duplicate ERA5 day %s", key)
		}
		seen[key] = struct{}{}
		if math.Abs(day.Metrics.Latitude-cell.GridLatitude) > 1e-9 || math.Abs(day.Metrics.Longitude-cell.GridLongitude) > 1e-9 {
			return Report{}, fmt.Errorf("ERA5 day %s coordinates do not match cell %s", day.Metrics.Date, cell.ID)
		}
		if day.Metrics.HumiditySaturationClampedHours < 0 || day.Metrics.HumiditySaturationClampedHours > input.Window.HourlyIntervalsPerDay || day.Metrics.MaximumDewPointExcessC < 0 || day.Metrics.MaximumDewPointExcessC > MaximumDewPointExcessC || (day.Metrics.HumiditySaturationClampedHours == 0) != (day.Metrics.MaximumDewPointExcessC == 0) {
			return Report{}, fmt.Errorf("ERA5 day %s/%s has invalid humidity saturation evidence", day.CellID, day.Metrics.Date)
		}
		humidityRepair.SaturationClampedHours += day.Metrics.HumiditySaturationClampedHours
		humidityRepair.MaximumDewPointExcessC = math.Max(humidityRepair.MaximumDewPointExcessC, day.Metrics.MaximumDewPointExcessC)
		metrics, err := day.Metrics.metrics()
		if err != nil {
			return Report{}, fmt.Errorf("ERA5 day %s/%s: %w", day.CellID, day.Metrics.Date, err)
		}
		nice, cycling, err := weather.ScoreProfilesChecked(metrics)
		if err != nil {
			return Report{}, fmt.Errorf("score ERA5 day %s/%s: %w", day.CellID, day.Metrics.Date, err)
		}
		scored = append(scored, ScoredDay{CellID: day.CellID, Metrics: day.Metrics, Nice: score(nice), Cycling: score(cycling), niceResult: nice, cyclingResult: cycling})
	}
	if err := validateCoverage(input, seen); err != nil {
		return Report{}, err
	}

	orderedCells := append([]Cell(nil), input.Cells...)
	sort.Slice(orderedCells, func(i, j int) bool { return orderedCells[i].ID < orderedCells[j].ID })
	seam, err := summarizeSeam(input.Year, orderedCells, scored)
	if err != nil {
		return Report{}, err
	}
	if len(seam) == 0 {
		return Report{}, errors.New("ERA5 canary produced no mandatory Chad boundary summary")
	}
	seamParts := make([]string, 0, len(seam))
	for _, item := range seam {
		seamParts = append(seamParts, fmt.Sprintf("%s: %d points", time.Month(item.Month), item.MaximumBoundaryOverallScoreDelta))
	}
	seamClaim := "Across 22.25-22.5 degrees east, the maximum daily overall-score deltas were " + strings.Join(seamParts, "; ") + "."
	scenarios, err := summarizeScenarios(orderedCells, scored)
	if err != nil {
		return Report{}, err
	}
	malagaMonths := 0
	for _, scenario := range scenarios {
		if scenario.CellID == "malaga" {
			malagaMonths++
		}
	}
	if malagaMonths != 12 {
		return Report{}, fmt.Errorf("ERA5 canary produced %d Malaga monthly summaries, want 12", malagaMonths)
	}
	return Report{
		SchemaVersion: ReportSchemaVersion,
		Source:        input.Source, Window: input.Window, Year: input.Year, Retrieval: input.Retrieval,
		Scope: Scope{
			Proves: []string{
				fmt.Sprintf("The extractor produced complete declared daily aggregates from ten selected hourly intervals for %d cells in %d.", len(input.Cells), input.Year),
				"The Go evaluator validated every daily aggregate and applied the unchanged canonical scorer.",
				seamClaim,
			},
			DoesNotProve: []string{
				"One year does not establish a recent multi-year climate distribution.",
				fmt.Sprintf("%d cells do not establish worldwide-month transfer, memory or build cost.", len(input.Cells)),
				"This report does not approve a production source, scoring or user-interface policy.",
				"This report does not establish scalar-tile and selected-place parity.",
			},
		},
		ScorePolicy: "canonical-weather-score-current-unmodified",
		Cells:       orderedCells, Scenarios: scenarios, Seam: seam, Days: scored, HumidityRepair: humidityRepair,
	}, nil
}

func validateInput(input Input) error {
	if input.SchemaVersion != InputSchemaVersion {
		return fmt.Errorf("ERA5 canary schema version is %d, want %d", input.SchemaVersion, InputSchemaVersion)
	}
	if input.Source.SnapshotID != PinnedSnapshotID || input.Source.Authority != "Copernicus Climate Change Service" || input.Source.Dataset != "ERA5 hourly data on single levels" || input.Source.Repository != "s3://earthmover-icechunk-era5/icechunkV2" || input.Source.Transport != "Earthmover Icechunk public mirror" || input.Source.GridDegrees != .25 || input.Source.NativeResolutionKM != 30 {
		return errors.New("ERA5 canary source identity is not the pinned Copernicus-authoritative 0.25 degree sample")
	}
	requiredVariables := []Variable{
		{Name: "t2m", Units: "K", Semantics: "instant"},
		{Name: "d2m", Units: "K", Semantics: "instant"},
		{Name: "tp", Units: "m", Semantics: "one-hour accumulation ending at valid_time"},
		{Name: "ssrd", Units: "J m**-2", Semantics: "one-hour accumulation ending at valid_time"},
		{Name: "tcc", Units: "0-1", Semantics: "instant"},
		{Name: "u10", Units: "m s**-1", Semantics: "instant"},
		{Name: "v10", Units: "m s**-1", Semantics: "instant"},
	}
	if !slices.Equal(input.Source.Variables, requiredVariables) {
		return fmt.Errorf("ERA5 canary variable table does not match the pinned contract")
	}
	if input.Window.ID != "civil-local-10-20" || input.Window.StartLocalHour != 10 || input.Window.EndLocalHour != 20 || input.Window.HourlyIntervalsPerDay != 10 || input.Window.WetHourlyThresholdMM != .1 {
		return errors.New("ERA5 canary must use the explicit civil-local 10:00-20:00 window")
	}
	if input.Year < 1940 || input.Year > 2025 || len(input.Cells) == 0 || len(input.Cells) > MaximumCells || len(input.Days) == 0 || len(input.Days) > MaximumDays {
		return errors.New("ERA5 canary year, cells and days are required")
	}
	if input.Retrieval.RequestCount <= 0 || input.Retrieval.RequestCount > MaximumRequests || input.Retrieval.RequestCount != input.Retrieval.GetCount+input.Retrieval.HeadCount || input.Retrieval.GetCount <= 0 || input.Retrieval.ResponseBodyBytes <= 0 || input.Retrieval.ResponseBodyBytes > MaximumTransferBytes || input.Retrieval.MaximumResponseBodyBytes <= 0 || input.Retrieval.MaximumResponseBodyBytes > MaximumResponseBytes || input.Retrieval.LogicalArrayReads <= 0 || input.Retrieval.DecodedArrayBytes <= 0 || input.Retrieval.SourceScratchPeakBytes < 0 || input.Retrieval.SourceScratchEndBytes < 0 || input.Retrieval.SourceScratchEndBytes > input.Retrieval.SourceScratchPeakBytes || input.Retrieval.PeakProcessRSSBytes <= 0 || input.Retrieval.WallTimeMilliseconds <= 0 {
		return errors.New("ERA5 canary retrieval measurements are incomplete")
	}
	seen := make(map[string]struct{}, len(input.Cells))
	for _, cell := range input.Cells {
		if cell.ID == "" || cell.Scenario == "" || cell.Timezone == "" || len(cell.Months) == 0 {
			return errors.New("ERA5 canary cell identity, scenario, timezone and months are required")
		}
		if _, duplicate := seen[cell.ID]; duplicate {
			return fmt.Errorf("duplicate ERA5 canary cell %q", cell.ID)
		}
		seen[cell.ID] = struct{}{}
		if _, err := time.LoadLocation(cell.Timezone); err != nil {
			return fmt.Errorf("ERA5 canary cell %s timezone: %w", cell.ID, err)
		}
		for _, month := range cell.Months {
			if month < 1 || month > 12 {
				return fmt.Errorf("ERA5 canary cell %s has invalid month %d", cell.ID, month)
			}
		}
	}
	malaga, ok := cellByID(input.Cells, "malaga")
	if !ok || malaga.Scenario != "malaga-annual-cycle" || !slices.Equal(malaga.Months, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}) {
		return errors.New("ERA5 canary requires the complete Malaga annual cycle")
	}
	return nil
}

func cellByID(cells []Cell, id string) (Cell, bool) {
	for _, cell := range cells {
		if cell.ID == id {
			return cell, true
		}
	}
	return Cell{}, false
}

func validateCoverage(input Input, seen map[string]struct{}) error {
	for _, cell := range input.Cells {
		for _, month := range cell.Months {
			last := time.Date(input.Year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
			for day := 1; day <= last; day++ {
				date := time.Date(input.Year, time.Month(month), day, 0, 0, 0, 0, time.UTC).Format(time.DateOnly)
				if _, ok := seen[cell.ID+"/"+date]; !ok {
					return fmt.Errorf("ERA5 canary is missing %s/%s", cell.ID, date)
				}
			}
		}
	}
	return nil
}

func score(result weather.Result) Score {
	return Score{Overall: result.Score, Rating: result.Rating, Comfort: result.Components.Thermal, Dry: result.Components.Dryness, Bright: result.Components.Sun, Calm: result.Components.Wind}
}

func summarizeScenarios(cells []Cell, days []ScoredDay) ([]ScenarioSummary, error) {
	result := make([]ScenarioSummary, 0)
	for _, cell := range cells {
		for _, month := range cell.Months {
			var selected []ScoredDay
			for _, day := range days {
				date, _ := time.Parse(time.DateOnly, day.Metrics.Date)
				if day.CellID == cell.ID && int(date.Month()) == month {
					selected = append(selected, day)
				}
			}
			summary, err := scenarioSummary(cell, month, selected)
			if err != nil {
				return nil, fmt.Errorf("summarize %s month %d: %w", cell.ID, month, err)
			}
			result = append(result, summary)
		}
	}
	return result, nil
}

func scenarioSummary(cell Cell, month int, days []ScoredDay) (ScenarioSummary, error) {
	niceResults, cyclingResults := make([]weather.Result, 0, len(days)), make([]weather.Result, 0, len(days))
	temperature, dewPoint, humidity, rain, wet, solar, cloud, wind := make([]float64, 0, len(days)), make([]float64, 0, len(days)), make([]float64, 0, len(days)), make([]float64, 0, len(days)), make([]float64, 0, len(days)), make([]float64, 0, len(days)), make([]float64, 0, len(days)), make([]float64, 0, len(days))
	for _, day := range days {
		niceResults, cyclingResults = append(niceResults, day.niceResult), append(cyclingResults, day.cyclingResult)
		temperature = append(temperature, day.Metrics.ComfortableTempC)
		dewPoint = append(dewPoint, day.Metrics.DewPointC)
		humidity = append(humidity, day.Metrics.RelativeHumidityPercent)
		rain = append(rain, day.Metrics.RainMM)
		wet = append(wet, day.Metrics.WetTimeFraction)
		solar = append(solar, day.Metrics.SolarEnergyMJm2)
		cloud = append(cloud, day.Metrics.CloudCoverPercent)
		wind = append(wind, day.Metrics.WindSpeedKPH)
	}
	nice, err := weather.SummarizeRange(niceResults)
	if err != nil {
		return ScenarioSummary{}, err
	}
	cycling, err := weather.SummarizeRange(cyclingResults)
	if err != nil {
		return ScenarioSummary{}, err
	}
	return ScenarioSummary{
		CellID: cell.ID, Scenario: cell.Scenario, Month: month, Days: len(days),
		NiceDaysPercent: percentOf(nice.NiceDays, nice.Days), AwfulDaysPercent: percentOf(nice.AwfulDays, nice.Days),
		MedianNiceScore: nice.MedianScore, MedianCyclingScore: cycling.MedianScore,
		Physical: PhysicalRange{DaytimeTemperatureC: valueRange(temperature), DewPointC: valueRange(dewPoint), RelativeHumidityPercent: valueRange(humidity), RainMM: valueRange(rain), WetTimeFraction: valueRange(wet), SolarEnergyMJm2: valueRange(solar), CloudCoverPercent: valueRange(cloud), WindSpeedKPH: valueRange(wind)},
	}, nil
}

func summarizeSeam(year int, cells []Cell, days []ScoredDay) ([]SeamSummary, error) {
	var seamCells []Cell
	for _, cell := range cells {
		if cell.Scenario == "chad-seam" {
			seamCells = append(seamCells, cell)
		}
	}
	sort.Slice(seamCells, func(i, j int) bool { return seamCells[i].GridLongitude < seamCells[j].GridLongitude })
	if len(seamCells) < 2 {
		return nil, nil
	}
	boundaryPairPresent := false
	for index := 0; index+1 < len(seamCells); index++ {
		if seamCells[index].GridLongitude == 22.25 && seamCells[index+1].GridLongitude == 22.5 {
			boundaryPairPresent = true
			break
		}
	}
	if !boundaryPairPresent {
		return nil, errors.New("ERA5 Chad seam scenario lacks the adjacent 22.25 and 22.5 degree east boundary cells")
	}
	byKey := make(map[string]ScoredDay)
	for _, day := range days {
		byKey[day.CellID+"/"+day.Metrics.Date] = day
	}
	monthCounts := make(map[int]int)
	for _, cell := range seamCells {
		for _, month := range cell.Months {
			monthCounts[month]++
		}
	}
	var commonMonths []int
	for month, count := range monthCounts {
		if count == len(seamCells) {
			commonMonths = append(commonMonths, month)
		}
	}
	sort.Ints(commonMonths)
	var result []SeamSummary
	for _, month := range commonMonths {
		summary := SeamSummary{Month: month, CellCount: len(seamCells), BoundaryWestLongitude: 22.25, BoundaryEastLongitude: 22.5}
		last := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
		for dayNumber := 1; dayNumber <= last; dayNumber++ {
			date := time.Date(year, time.Month(month), dayNumber, 0, 0, 0, 0, time.UTC).Format(time.DateOnly)
			for index := 0; index+1 < len(seamCells); index++ {
				west, westOK := byKey[seamCells[index].ID+"/"+date]
				east, eastOK := byKey[seamCells[index+1].ID+"/"+date]
				if !westOK || !eastOK {
					return nil, fmt.Errorf("ERA5 seam evidence is missing for %s", date)
				}
				temperatureDelta := math.Abs(west.Metrics.ComfortableTempC - east.Metrics.ComfortableTempC)
				comfortDelta := abs(west.Nice.Comfort - east.Nice.Comfort)
				overallDelta := abs(west.Nice.Overall - east.Nice.Overall)
				if seamCells[index].GridLongitude == summary.BoundaryWestLongitude && seamCells[index+1].GridLongitude == summary.BoundaryEastLongitude {
					if temperatureDelta > summary.MaximumBoundaryTemperatureDeltaC {
						summary.MaximumBoundaryTemperatureDeltaC = temperatureDelta
						summary.MaximumBoundaryTemperatureDate = west.Metrics.Date
					}
					if overallDelta > summary.MaximumBoundaryOverallScoreDelta {
						summary.MaximumBoundaryOverallScoreDelta = overallDelta
						summary.MaximumBoundaryOverallDate = west.Metrics.Date
					}
					if comfortDelta > summary.MaximumBoundaryComfortScoreDelta {
						summary.MaximumBoundaryComfortScoreDelta = comfortDelta
						summary.MaximumBoundaryComfortDate = west.Metrics.Date
					}
				}
				if temperatureDelta > summary.MaximumAdjacentTemperatureDeltaC {
					summary.MaximumAdjacentTemperatureDeltaC = temperatureDelta
					summary.MaximumAdjacentTemperatureDate = west.Metrics.Date
					summary.MaximumAdjacentTemperatureWestLongitude = seamCells[index].GridLongitude
					summary.MaximumAdjacentTemperatureEastLongitude = seamCells[index+1].GridLongitude
				}
				if overallDelta > summary.MaximumAdjacentOverallScoreDelta {
					summary.MaximumAdjacentOverallScoreDelta = overallDelta
					summary.MaximumAdjacentOverallDate = west.Metrics.Date
					summary.MaximumAdjacentOverallWestLongitude = seamCells[index].GridLongitude
					summary.MaximumAdjacentOverallEastLongitude = seamCells[index+1].GridLongitude
				}
				if comfortDelta > summary.MaximumAdjacentComfortScoreDelta {
					summary.MaximumAdjacentComfortScoreDelta = comfortDelta
					summary.MaximumAdjacentComfortDate = west.Metrics.Date
					summary.MaximumAdjacentComfortWestLongitude = seamCells[index].GridLongitude
					summary.MaximumAdjacentComfortEastLongitude = seamCells[index+1].GridLongitude
				}
			}
		}
		result = append(result, summary)
	}
	return result, nil
}

func valueRange(values []float64) ValueRange {
	if len(values) == 0 {
		return ValueRange{}
	}
	minimum, maximum, sum := values[0], values[0], 0.0
	for _, value := range values {
		minimum = math.Min(minimum, value)
		maximum = math.Max(maximum, value)
		sum += value
	}
	return ValueRange{Minimum: round6(minimum), Mean: round6(sum / float64(len(values))), Maximum: round6(maximum)}
}

func percentOf(count, total int) int {
	return int(math.Round(float64(count) * 100 / float64(total)))
}
func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
func round6(value float64) float64 { return math.Round(value*1_000_000) / 1_000_000 }
