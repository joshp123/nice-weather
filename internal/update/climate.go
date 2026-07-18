package update

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"strings"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/weather"
)

const chelsaRoot = "https://os.zhdk.cloud.switch.ch/chelsav2/GLOBAL/climatologies/1981-2010"

type chelsaMetadataUnitPolicy uint8

const (
	// CHELSA's GeoTIFF unit strings are absent or not reliable enough to be a
	// source contract. Scale, offset, and the transformed physical range are.
	chelsaUnitUnreliable chelsaMetadataUnitPolicy = iota
)

type chelsaVariableSpec struct {
	ID                 string
	SolarFilenameOrder bool
	MetadataUnit       string
	MetadataUnitPolicy chelsaMetadataUnitPolicy
	Scale              float64
	Offset             float64
	Minimum            float64
	Maximum            float64
}

var chelsaVariableSpecs = []chelsaVariableSpec{
	{ID: "tas", MetadataUnitPolicy: chelsaUnitUnreliable, Scale: .1, Offset: -273.15, Minimum: -80, Maximum: 70},
	{ID: "tasmin", MetadataUnitPolicy: chelsaUnitUnreliable, Scale: .1, Offset: -273.15, Minimum: -80, Maximum: 70},
	{ID: "tasmax", MetadataUnitPolicy: chelsaUnitUnreliable, Scale: .1, Offset: -273.15, Minimum: -80, Maximum: 70},
	{ID: "pr", MetadataUnitPolicy: chelsaUnitUnreliable, Scale: .1, Minimum: 0, Maximum: 5000},
	{ID: "sfcWind", MetadataUnitPolicy: chelsaUnitUnreliable, Scale: .001, Minimum: 0, Maximum: 100},
	{ID: "clt", MetadataUnitPolicy: chelsaUnitUnreliable, Scale: .01, Minimum: 0, Maximum: 100},
	{ID: "rsds", SolarFilenameOrder: true, MetadataUnitPolicy: chelsaUnitUnreliable, Scale: .001, Minimum: 0, Maximum: 60},
	{ID: "hurs", MetadataUnitPolicy: chelsaUnitUnreliable, Scale: .01, Minimum: 0, Maximum: 100},
}

var climateVariables = func() []string {
	variables := make([]string, len(chelsaVariableSpecs))
	for index, spec := range chelsaVariableSpecs {
		variables[index] = spec.ID
	}
	return variables
}()

const maximumPartialClimateSupportFraction = 0.02

func chelsaVariable(variable string) (chelsaVariableSpec, error) {
	for _, spec := range chelsaVariableSpecs {
		if spec.ID == variable {
			return spec, nil
		}
	}
	return chelsaVariableSpec{}, fmt.Errorf("unknown CHELSA variable %q", variable)
}

func chelsaURL(variable string, month int) string {
	spec, err := chelsaVariable(variable)
	if err != nil {
		return ""
	}
	if spec.SolarFilenameOrder {
		return fmt.Sprintf("%s/%s/CHELSA_%s_1981-2010_%02d_V.2.1.tif", chelsaRoot, variable, variable, month)
	}
	return fmt.Sprintf("%s/%s/CHELSA_%s_%02d_1981-2010_V.2.1.tif", chelsaRoot, variable, variable, month)
}

func buildClimate(ctx context.Context, decoder *pipeline.Decoder, writer *releaseWriter, cacheRoot string, land *LandAuthority) error {
	for _, variable := range climateVariables {
		operation, cancel := context.WithTimeout(ctx, 3*time.Minute)
		err := verifyCHELSAMetadata(operation, variable, 1)
		cancel()
		if err != nil {
			return err
		}
	}
	for month := 1; month <= 12; month++ {
		cached, ok, err := loadClimateCheckpoint(cacheRoot, writer.dir, month)
		if err != nil {
			return err
		}
		if ok {
			writer.manifest.Climate.Months = append(writer.manifest.Climate.Months, cached)
			fmt.Printf("climate month %02d/12 reused\n", month)
			continue
		}
		grids, support, err := decodeClimateMonth(ctx, decoder, land, month)
		if err != nil {
			return err
		}
		fmt.Printf("climate month %02d support: %s\n", month, support.String())
		metrics, err := climateMetrics(month, grids)
		if err != nil {
			return err
		}
		id := fmt.Sprintf("month-%02d", month)
		source := delivery.Source{
			ProviderID: "chelsa", ProviderLabel: "CHELSA 2.1 · CC0",
			RunAt: "1981-01-01T00:00:00Z", ValidFrom: "1981-01-01T00:00:00Z", ValidTo: "2011-01-01T00:00:00Z",
		}
		period, err := writer.writePeriod("climate", id, time.Month(month).String(), grids["tas"].Spec, metrics, source, 11, 24*30)
		if err != nil {
			return err
		}
		climatePeriod := delivery.ClimatePeriod{Period: period, Month: month}
		writer.manifest.Climate.Months = append(writer.manifest.Climate.Months, climatePeriod)
		if err := saveClimateCheckpoint(cacheRoot, writer.dir, climatePeriod); err != nil {
			return fmt.Errorf("checkpoint climate month %02d: %w", month, err)
		}
		fmt.Printf("climate month %02d/12 ready\n", month)
	}
	return nil
}

type climateSupportReport struct {
	CoreCells   int
	CoreMissing int
	EdgeCells   int
	EdgeMissing int
	CoreSamples []string
	EdgeSamples []string
}

func (report climateSupportReport) String() string {
	detail := fmt.Sprintf("core %d/%d missing; touched edge %d/%d missing", report.CoreMissing, report.CoreCells, report.EdgeMissing, report.EdgeCells)
	if len(report.CoreSamples) > 0 {
		detail += "; core samples: " + strings.Join(report.CoreSamples, "; ")
	}
	if len(report.EdgeSamples) > 0 {
		detail += "; edge samples: " + strings.Join(report.EdgeSamples, "; ")
	}
	return detail
}

func decodeClimateMonth(ctx context.Context, decoder *pipeline.Decoder, land *LandAuthority, month int) (map[string]pipeline.FloatGrid, climateSupportReport, error) {
	grids := make(map[string]pipeline.FloatGrid, len(climateVariables))
	for _, variable := range climateVariables {
		operation, cancel := context.WithTimeout(ctx, 3*time.Minute)
		grid, err := decoder.DecodeRaster(operation, "/vsicurl/"+chelsaURL(variable, month), .1)
		cancel()
		if err != nil {
			return nil, climateSupportReport{}, fmt.Errorf("CHELSA %s month %d: %w", variable, month, err)
		}
		if err := validateDecodedCHELSAGrid(month, variable, grid); err != nil {
			return nil, climateSupportReport{}, err
		}
		grids[variable] = grid
	}
	allTouched, err := land.Mask(ctx, grids["tas"].Spec)
	if err != nil {
		return nil, climateSupportReport{}, fmt.Errorf("Natural Earth all-touched climate mask: %w", err)
	}
	centres, err := land.CenterMask(ctx, grids["tas"].Spec)
	if err != nil {
		return nil, climateSupportReport{}, fmt.Errorf("Natural Earth centre climate mask: %w", err)
	}
	report, err := canonicalizeClimateSupport(month, grids, centres, allTouched)
	if err != nil {
		return nil, report, err
	}
	return grids, report, nil
}

// canonicalizeClimateSupport applies the explicit Natural Earth land authority.
// A land cell is publishable only when every CHELSA source can describe it.
func canonicalizeClimateSupport(month int, grids map[string]pipeline.FloatGrid, centres, allTouched pipeline.FloatGrid) (climateSupportReport, error) {
	reference, ok := grids["tas"]
	if !ok {
		return climateSupportReport{}, fmt.Errorf("CHELSA temperature grid is missing")
	}
	for _, variable := range climateVariables {
		grid, exists := grids[variable]
		if !exists || grid.Spec != reference.Spec || len(grid.Values) != len(reference.Values) {
			return climateSupportReport{}, fmt.Errorf("CHELSA %s grid does not match temperature grid", variable)
		}
	}
	if centres.Spec != reference.Spec || allTouched.Spec != reference.Spec || len(centres.Values) != len(reference.Values) || len(allTouched.Values) != len(reference.Values) {
		return climateSupportReport{}, fmt.Errorf("Natural Earth masks do not match climate grid")
	}
	report := climateSupportReport{CoreSamples: make([]string, 0, 3), EdgeSamples: make([]string, 0, 3)}
	for index := range reference.Values {
		centre, touched := centres.Values[index], allTouched.Values[index]
		if (centre != 0 && centre != 1) || (touched != 0 && touched != 1) || centre > touched {
			return climateSupportReport{}, fmt.Errorf("Natural Earth mask topology is invalid at cell %d: centre=%g all-touched=%g", index, centre, touched)
		}
		if touched == 0 {
			for _, variable := range climateVariables {
				grid := grids[variable]
				grid.Values[index] = float32(math.NaN())
				grids[variable] = grid
			}
			continue
		}
		core := centre == 1
		if core {
			report.CoreCells++
		} else {
			report.EdgeCells++
		}
		missing := make([]string, 0, 2)
		for _, variable := range climateVariables {
			value := grids[variable].Values[index]
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				missing = append(missing, variable)
			}
		}
		if len(missing) == 0 {
			continue
		}
		row, col, lat, lon := climateCellLocation(index, reference.Spec)
		sample := fmt.Sprintf("cell %d row %d col %d %.5f,%.5f missing %s", index, row, col, lat, lon, strings.Join(missing, ","))
		if core {
			report.CoreMissing++
			if len(report.CoreSamples) < cap(report.CoreSamples) {
				report.CoreSamples = append(report.CoreSamples, sample)
			}
		} else {
			report.EdgeMissing++
			if len(report.EdgeSamples) < cap(report.EdgeSamples) {
				report.EdgeSamples = append(report.EdgeSamples, sample)
			}
		}
		for _, variable := range climateVariables {
			grid := grids[variable]
			grid.Values[index] = float32(math.NaN())
			grids[variable] = grid
		}
	}
	// This is a percentage guard, not a "permit at least one" rule. Floor is
	// intentional: one missing cell out of 49 is already more than 2%.
	maximum := int(math.Floor(float64(report.CoreCells) * maximumPartialClimateSupportFraction))
	if report.CoreMissing > maximum {
		return report, fmt.Errorf("CHELSA month %02d support is incompatible: %s; maximum missing-support cells is %d", month, report.String(), maximum)
	}
	return report, nil
}

func climateMetrics(month int, grids map[string]pipeline.FloatGrid) ([]weather.DailyMetrics, error) {
	reference, ok := grids["tas"]
	if !ok {
		return nil, fmt.Errorf("CHELSA temperature grid is missing")
	}
	for _, variable := range climateVariables {
		grid, exists := grids[variable]
		if !exists || grid.Spec != reference.Spec || len(grid.Values) != len(reference.Values) {
			return nil, fmt.Errorf("CHELSA %s grid does not match temperature grid", variable)
		}
		if err := validateDecodedCHELSAGrid(month, variable, grid); err != nil {
			return nil, err
		}
	}
	days := float64(time.Date(2001, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day())
	metrics := make([]weather.DailyMetrics, len(reference.Values))
	for index := range metrics {
		values := make(map[string]float64, len(climateVariables))
		finiteValues := 0
		for _, variable := range climateVariables {
			value := float64(grids[variable].Values[index])
			values[variable] = value
			if math.IsNaN(value) || math.IsInf(value, 0) {
				continue
			}
			finiteValues++
		}
		if finiteValues == 0 {
			// CHELSA uses non-finite cells for ocean. They intentionally remain
			// invalid so they are transparent in the published product.
			continue
		}
		if finiteValues != len(climateVariables) {
			return nil, climateCellError(month, index, reference.Spec, values, "source rasters disagree on whether the cell is land")
		}
		// gdalwarp preserves stored COG samples. These transforms are checked
		// against live band metadata by the source smoke before a real build.
		meanTemp := scaleCHELSA("tas", values["tas"])
		minimum := scaleCHELSA("tasmin", values["tasmin"])
		maximum := scaleCHELSA("tasmax", values["tasmax"])
		// The midpoint between monthly all-day mean and daily maximum is the
		// explicit generic proxy for the warmer 10:00–20:00 product window.
		comfortable := (meanTemp + maximum) / 2
		dailyRain := scaleCHELSA("pr", values["pr"]) / days
		windKPH := scaleCHELSA("sfcWind", values["sfcWind"]) * 3.6
		cloud := scaleCHELSA("clt", values["clt"])
		humidity := scaleCHELSA("hurs", values["hurs"])
		// The stored climatology values decode to plausible daily energy
		// (0–15.4 MJ/m² in January Europe), matching CHELSA's monthly catalog.
		// Do not reinterpret them as an instantaneous W/m² flux.
		dailySolar := scaleCHELSA("rsds", values["rsds"])
		metric := weather.DailyMetrics{
			ComfortableTempC: comfortable, MinTempC: minimum, MaxTempC: maximum,
			RainMM:                  dailyRain,
			SolarEnergyMJm2:         weather.OptionalFloat{Value: dailySolar, Available: true},
			CloudCoverPercent:       weather.OptionalFloat{Value: cloud, Available: true},
			WindSpeedKPH:            windKPH,
			RelativeHumidityPercent: weather.OptionalFloat{Value: humidity, Available: true},
			NativeResolutionKM:      11, Valid: true,
		}
		if err := metric.Validate(); err != nil {
			return nil, climateCellError(month, index, reference.Spec, values, err.Error())
		}
		metrics[index] = metric
	}
	return metrics, nil
}

func scaleCHELSA(variable string, stored float64) float64 {
	spec, err := chelsaVariable(variable)
	if err != nil {
		return math.NaN()
	}
	return stored*spec.Scale + spec.Offset
}

func validateDecodedCHELSAGrid(month int, variable string, grid pipeline.FloatGrid) error {
	spec, err := chelsaVariable(variable)
	if err != nil {
		return err
	}
	finite := 0
	for index, stored := range grid.Values {
		if math.IsNaN(float64(stored)) {
			continue
		}
		finite++
		transformed := float64(stored)*spec.Scale + spec.Offset
		if math.IsNaN(transformed) || math.IsInf(transformed, 0) || transformed < spec.Minimum || transformed > spec.Maximum {
			row, col, lat, lon := climateCellLocation(index, grid.Spec)
			return fmt.Errorf("CHELSA %s month %02d cell %d (row %d col %d, %.5f,%.5f) transformed value %.6g from stored %.6g is outside %.1f..%.1f", variable, month, index, row, col, lat, lon, transformed, stored, spec.Minimum, spec.Maximum)
		}
	}
	if finite == 0 {
		return fmt.Errorf("CHELSA %s month %02d contains no finite cells", variable, month)
	}
	return nil
}

func climateCellError(month, index int, spec delivery.GridSpec, values map[string]float64, reason string) error {
	row, col, lat, lon := climateCellLocation(index, spec)
	return fmt.Errorf("CHELSA month %02d cell %d (row %d col %d, %.5f,%.5f) is invalid: %s; stored values tas=%g tasmin=%g tasmax=%g pr=%g sfcWind=%g clt=%g rsds=%g hurs=%g",
		month, index, row, col, lat, lon, reason,
		values["tas"], values["tasmin"], values["tasmax"], values["pr"], values["sfcWind"], values["clt"], values["rsds"], values["hurs"])
}

func climateCellLocation(index int, spec delivery.GridSpec) (int, int, float64, float64) {
	cols := int(spec.Cols)
	if cols == 0 || spec.Rows == 0 {
		return 0, 0, math.NaN(), math.NaN()
	}
	row, col := index/cols, index%cols
	width := (spec.Bounds.East - spec.Bounds.West) / float64(spec.Cols)
	height := (spec.Bounds.North - spec.Bounds.South) / float64(spec.Rows)
	lon := spec.Bounds.West + (float64(col)+.5)*width
	lat := spec.Bounds.North - (float64(row)+.5)*height
	return row, col, lat, lon
}

type rasterBandMetadata struct {
	Bands []struct {
		Scale  float64 `json:"scale"`
		Offset float64 `json:"offset"`
		Unit   string  `json:"unit"`
	} `json:"bands"`
}

func verifyCHELSAMetadata(ctx context.Context, variable string, month int) error {
	output, err := exec.CommandContext(ctx, "gdalinfo", "-json", "/vsicurl/"+chelsaURL(variable, month)).Output()
	if err != nil {
		return fmt.Errorf("inspect CHELSA %s metadata: %w", variable, err)
	}
	var metadata rasterBandMetadata
	if err := json.Unmarshal(output, &metadata); err != nil || len(metadata.Bands) != 1 {
		return fmt.Errorf("CHELSA %s has invalid band metadata", variable)
	}
	spec, err := chelsaVariable(variable)
	if err != nil {
		return err
	}
	band := metadata.Bands[0]
	if math.Abs(band.Scale-spec.Scale) > 1e-9 || math.Abs(band.Offset-spec.Offset) > 1e-9 {
		return fmt.Errorf("CHELSA %s scale/offset changed: got %g/%g want %g/%g", variable, band.Scale, band.Offset, spec.Scale, spec.Offset)
	}
	if spec.MetadataUnitPolicy != chelsaUnitUnreliable || spec.MetadataUnit != "" {
		return fmt.Errorf("CHELSA %s has unsupported metadata unit policy", variable)
	}
	return nil
}
