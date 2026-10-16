// Package dailyforecast turns one ECMWF IFS run into daily map images and point records.
package dailyforecast

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ringsaturn/tzf"
	"nice-weather-finder/internal/providers/ifs"
	"nice-weather-finder/internal/providers/ifsgrib"
)

const (
	ResolutionDegrees = 0.25
	Width             = int(360 / ResolutionDegrees)
	Height            = int(180/ResolutionDegrees) + 1
	PointRecordBytes  = 17
	noData            = 255
)

var Layers = []string{"nice", "cycling", "comfort", "dry", "clear", "calm-nice", "calm-cycling"}
var fields = []string{"2t", "2d", "tcc", "10u", "10v", "tp"}

// PointRecord is little-endian: temperature i16 at 0, dew point i16 at 2,
// rain u16 at 4 (all tenths); wet%, cloud%, wind km/h, gust km/h at 6..9;
// seven scores in Layers order at 10..16. Missing records are all 0xff.
// Missing gust alone is 255; missing signed temperatures are -1 (0xffff).
type PointRecord struct {
	TemperatureTenthsC int16
	DewPointTenthsC    int16
	RainTenthsMM       uint16
	WetPercent         uint8
	CloudPercent       uint8
	WindKPH            uint8
	GustKPH            uint8
	Scores             [7]uint8
}

type Grid struct {
	Width        int     `json:"width"`
	Height       int     `json:"height"`
	West         float64 `json:"west"`
	North        float64 `json:"north"`
	Resolution   float64 `json:"resolution"`
	Registration string  `json:"registration"`
	RowOrder     string  `json:"rowOrder"`
}

type Manifest struct {
	RunID            string    `json:"runId"`
	RunTime          time.Time `json:"runTime"`
	Grid             Grid      `json:"grid"`
	Dates            []string  `json:"dates"`
	Layers           []string  `json:"layers"`
	Profiles         []string  `json:"profiles"`
	PointRecordBytes int       `json:"pointRecordBytes"`
	NoData           int       `json:"noData"`
	Daytime          string    `json:"daytime"`
	BoundaryDates    string    `json:"boundaryDates"`
	Gust             string    `json:"gust"`
	ValidLandCells   []int     `json:"validLandCells"`
}

type Report struct {
	RunID           string
	RunTime         time.Time
	Dates           []string
	Directory       string
	DownloadedBytes int64
	OutputBytes     int64
	OutputFiles     int
	Elapsed         time.Duration
}

type forecastTransport struct {
	client *http.Client
	bytes  atomic.Int64
}
type countingBody struct {
	io.ReadCloser
	bytes *atomic.Int64
}

func (body countingBody) Read(buffer []byte) (int, error) {
	n, err := body.ReadCloser.Read(buffer)
	body.bytes.Add(int64(n))
	return n, err
}
func (transport *forecastTransport) Do(request *http.Request) (*http.Response, error) {
	response, err := transport.client.Do(request)
	if err == nil {
		response.Body = countingBody{response.Body, &transport.bytes}
	}
	return response, err
}

// Build writes a fresh forecast/<run-id> directory. A failed run is not published:
// manifest.json is written only after every image and point file is complete.
func Build(ctx context.Context, outputDirectory string, log io.Writer) (Report, error) {
	started := time.Now()
	if log == nil {
		log = io.Discard
	}
	transport := &forecastTransport{client: &http.Client{Timeout: 2 * time.Minute}}
	client := ifs.NewClient(transport)
	run, err := latestRun(ctx, client)
	if err != nil {
		return Report{}, err
	}
	report := Report{RunID: run.Time.Format("20060102T1504Z"), RunTime: run.Time}
	report.Directory = filepath.Join(outputDirectory, "forecast", report.RunID)
	if err = os.MkdirAll(filepath.Dir(report.Directory), 0755); err != nil {
		return report, err
	}
	if err = os.Mkdir(report.Directory, 0755); err != nil {
		return report, fmt.Errorf("create fresh run directory (choose a fresh --out when rerunning): %w", err)
	}
	fmt.Fprintf(log, "IFS run %s; grid %dx%d at %.2f°\n", run.Time.Format(time.RFC3339), Width, Height, ResolutionDegrees)
	land, err := landCells()
	if err != nil {
		return report, err
	}
	groups, err := timezoneGroups(land)
	if err != nil {
		return report, err
	}
	fmt.Fprintf(log, "land cells %d; civil time zones %d\n", len(land), len(groups))
	date := time.Date(run.Time.Year(), run.Time.Month(), run.Time.Day(), 0, 0, 0, 0, time.UTC)
	dates := make([]time.Time, 15)
	for day := range dates {
		dates[day] = date.AddDate(0, 0, day)
		report.Dates = append(report.Dates, dates[day].Format("2006-01-02"))
	}
	accumulators := make([][]dayAccumulator, len(dates))
	for day := range dates {
		accumulators[day] = make([]dayAccumulator, len(land))
	}
	steps, _ := ifs.ForecastSteps(run)
	var priorRain []float32
	var previousEnd time.Time
	adjustments := rainAdjustments{}
	for firstStepIndex := 0; firstStepIndex < len(steps); firstStepIndex += 2 {
		if err = ctx.Err(); err != nil {
			return report, err
		}
		var results [2]struct {
			frames [][]float32
			err    error
		}
		batchSize := min(2, len(steps)-firstStepIndex)
		var downloads sync.WaitGroup
		for slot := 0; slot < batchSize; slot++ {
			downloads.Add(1)
			go func(slot int) {
				defer downloads.Done()
				results[slot].frames, results[slot].err = downloadStep(ctx, client, run, steps[firstStepIndex+slot])
			}(slot)
		}
		downloads.Wait()
		for slot := 0; slot < batchSize; slot++ {
			if results[slot].err != nil {
				return report, results[slot].err
			}
			stepIndex := firstStepIndex + slot
			frames := results[slot].frames
			end := run.Time.Add(time.Duration(steps[stepIndex]) * time.Hour)
			if stepIndex > 0 {
				accumulateInterval(dates, groups, land, accumulators, frames, priorRain, &adjustments, previousEnd, end)
			}
			priorRain = frames[5]
			previousEnd = end
			if stepIndex%10 == 0 || stepIndex == len(steps)-1 {
				fmt.Fprintf(log, "downloaded and decoded %d/%d steps; %.1f MB received\n", stepIndex+1, len(steps), float64(transport.bytes.Load())/1e6)
			}
		}
	}
	fmt.Fprintf(log, "cumulative rain: %d negative adjustments; largest deficit %.6f mm\n", adjustments.count, adjustments.largestDeficitMM)
	manifest := Manifest{RunID: report.RunID, RunTime: run.Time, Grid: Grid{Width, Height, -180, 90, ResolutionDegrees, "grid-point", "north-to-south"}, Dates: report.Dates, Layers: Layers, Profiles: []string{"nice", "cycling"}, PointRecordBytes: PointRecordBytes, NoData: noData, Daytime: "10:00–20:00 IANA civil local time; requires the complete 10-hour window", BoundaryDates: "Cells without a complete daytime window at run boundaries are missing.", Gust: "Unavailable: IFS open-data gusts cover only one hour per 3/6-hour output interval, not a complete daytime maximum."}
	counts := [2][]byte{make([]byte, Width*Height), make([]byte, Width*Height)}
	completeDays := make([]byte, Width*Height)
	for profile := range counts {
		for cell := range counts[profile] {
			counts[profile][cell] = noData
		}
	}
	for day := range dates {
		valid, writeErr := writeDay(report.Directory, report.Dates[day], land, accumulators[day], counts, completeDays)
		if writeErr != nil {
			return report, writeErr
		}
		manifest.ValidLandCells = append(manifest.ValidLandCells, valid)
		fmt.Fprintf(log, "%s: %d/%d land cells with complete daytime\n", report.Dates[day], valid, len(land))
		accumulators[day] = nil
	}
	// A partial run-boundary day is missing, never silently counted as bad weather.
	for _, cell := range land {
		if completeDays[cell] == 0 {
			counts[0][cell] = noData
			counts[1][cell] = noData
		}
	}
	if err = os.Mkdir(filepath.Join(report.Directory, "fortnight"), 0755); err != nil {
		return report, err
	}
	for profile, name := range manifest.Profiles {
		if err = writePNG(filepath.Join(report.Directory, "fortnight", name+".png"), counts[profile]); err != nil {
			return report, err
		}
	}
	encoded, _ := json.MarshalIndent(manifest, "", "  ")
	if err = os.WriteFile(filepath.Join(report.Directory, "manifest.json"), append(encoded, '\n'), 0644); err != nil {
		return report, err
	}
	err = filepath.WalkDir(report.Directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil {
			return statErr
		}
		report.OutputFiles++
		report.OutputBytes += info.Size()
		return nil
	})
	report.DownloadedBytes = transport.bytes.Load()
	report.Elapsed = time.Since(started)
	return report, err
}

func latestRun(ctx context.Context, client *ifs.Client) (ifs.Run, error) {
	now := time.Now().UTC()
	candidate := time.Date(now.Year(), now.Month(), now.Day(), now.Hour()/12*12, 0, 0, 0, time.UTC)
	for attempt := 0; attempt < 8; attempt++ {
		run := ifs.Run{Time: candidate}
		messages, err := client.FetchIndex(ctx, run, 360)
		if err == nil {
			url, _ := client.GRIBURL(run, 360)
			request, _ := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
			response, probeErr := client.HTTP.Do(request)
			if probeErr != nil {
				return ifs.Run{}, probeErr
			}
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				for _, message := range messages {
					if response.ContentLength < message.Offset()+message.Length() {
						return ifs.Run{}, fmt.Errorf("incomplete IFS 360-hour GRIB object")
					}
				}
				return run, nil
			}
			if response.StatusCode != http.StatusNotFound {
				return ifs.Run{}, fmt.Errorf("probe IFS run: HTTP %d", response.StatusCode)
			}
		} else {
			var missing *ifs.NotPublishedError
			if !errors.As(err, &missing) {
				return ifs.Run{}, err
			}
		}
		candidate = candidate.Add(-12 * time.Hour)
	}
	return ifs.Run{}, errors.New("no complete IFS run found in the last four days")
}

type timezoneGroup struct {
	location    *time.Location
	landIndices []int
}

func timezoneGroups(land []int) ([]timezoneGroup, error) {
	finder, err := tzf.NewFullFinder()
	if err != nil {
		return nil, err
	}
	groups := []timezoneGroup{}
	byName := map[string]int{}
	for landIndex, cell := range land {
		longitude := -180 + float64(cell%Width)*ResolutionDegrees
		latitude := 90 - float64(cell/Width)*ResolutionDegrees
		name := finder.GetTimezoneName(longitude, latitude)
		if name == "" {
			// Polygon edges have no owner in tzf; probe either side of the
			// same native sample without moving its weather or land footprint.
			for _, offset := range [][2]float64{{1e-7, 1e-7}, {-1e-7, 1e-7}, {1e-7, -1e-7}, {-1e-7, -1e-7}} {
				queryLongitude := max(-180+1e-7, min(180-1e-7, longitude+offset[0]))
				queryLatitude := max(-90+1e-7, min(90-1e-7, latitude+offset[1]))
				name = finder.GetTimezoneName(queryLongitude, queryLatitude)
				if name != "" {
					break
				}
			}
		}
		if name == "" {
			return nil, fmt.Errorf("no IANA time zone at land cell %.2f,%.2f", latitude, longitude)
		}
		groupIndex, exists := byName[name]
		if !exists {
			location, loadErr := time.LoadLocation(name)
			if loadErr != nil {
				return nil, loadErr
			}
			groupIndex = len(groups)
			byName[name] = groupIndex
			groups = append(groups, timezoneGroup{location: location})
		}
		groups[groupIndex].landIndices = append(groups[groupIndex].landIndices, landIndex)
	}
	return groups, nil
}

func downloadStep(ctx context.Context, client *ifs.Client, run ifs.Run, step int) ([][]float32, error) {
	messages, err := client.FetchIndex(ctx, run, step)
	if err != nil {
		return nil, err
	}
	payloads := make([][]byte, len(fields))
	errorsByField := make([]error, len(fields))
	var workers sync.WaitGroup
	for fieldIndex, field := range fields {
		workers.Add(1)
		go func(index int, name string) {
			defer workers.Done()
			for attempt := 1; attempt <= 3; attempt++ {
				var buffer strings.Builder
				errorsByField[index] = client.DownloadIndexedMessage(ctx, run, step, messages[name], &buffer)
				if errorsByField[index] == nil {
					payloads[index] = []byte(buffer.String())
					return
				}
				if attempt == 3 {
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Duration(attempt) * time.Second):
				}
			}
		}(fieldIndex, field)
	}
	workers.Wait()
	values := make([][]float32, len(fields))
	for fieldIndex, name := range fields {
		if errorsByField[fieldIndex] != nil {
			return nil, errorsByField[fieldIndex]
		}
		field, decodeErr := ifsgrib.Decode(payloads[fieldIndex])
		if decodeErr != nil {
			return nil, decodeErr
		}
		if field.Name != name || !field.RunTime.Equal(run.Time) || field.EndStep != step {
			return nil, fmt.Errorf("IFS %s message does not match requested field, run and step", name)
		}
		values[fieldIndex] = make([]float32, len(field.Values))
		for cell, value := range field.Values {
			values[fieldIndex][cell] = float32(value)
		}
	}
	return values, nil
}
