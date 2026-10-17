// Package dailyforecast turns one ECMWF IFS run into v6 daily records and score images.
package dailyforecast

import (
	"context"
	"crypto/tls"
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
)

var fields = ifs.RequiredSurfaceFields()

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

// Build writes a fresh v6/forecast/<run-id> directory and switches the local pointer last.
func Build(ctx context.Context, outputDirectory, staticSource string, log io.Writer) (Report, error) {
	started := time.Now()
	if log == nil {
		log = io.Discard
	}
	httpTransport := http.DefaultTransport.(*http.Transport).Clone()
	httpTransport.TLSClientConfig = &tls.Config{NextProtos: []string{"http/1.1"}}
	httpTransport.Protocols = new(http.Protocols)
	httpTransport.Protocols.SetHTTP1(true)
	transport := &forecastTransport{client: &http.Client{Transport: httpTransport, Timeout: 2 * time.Minute}}
	client := ifs.NewClient(transport)
	v6, err := loadV6Static(ctx, staticSource)
	if err != nil {
		return Report{}, err
	}
	run, err := latestRun(ctx, client)
	if err != nil {
		return Report{}, err
	}
	report := Report{RunID: run.Time.Format("20060102T1504Z"), RunTime: run.Time}
	report.Directory = filepath.Join(outputDirectory, "v6", "forecast", report.RunID)
	if err = os.MkdirAll(filepath.Dir(report.Directory), 0755); err != nil {
		return report, err
	}
	if err = os.Mkdir(report.Directory, 0755); err != nil {
		return report, fmt.Errorf("create fresh run directory (choose a fresh --out when rerunning): %w", err)
	}
	fmt.Fprintf(log, "IFS run %s; grid %dx%d at %.2f°\n", run.Time.Format(time.RFC3339), Width, Height, ResolutionDegrees)
	for day := range v6Days {
		report.Dates = append(report.Dates, run.Time.AddDate(0, 0, day).Format("2006-01-02"))
	}
	steps, _ := ifs.ForecastSteps(run)
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
			v6.addFrame(end, frames)
			if stepIndex%10 == 0 || stepIndex == len(steps)-1 {
				fmt.Fprintf(log, "downloaded and decoded %d/%d steps; %.1f MB received\n", stepIndex+1, len(steps), float64(transport.bytes.Load())/1e6)
			}
		}
	}
	if err = v6.write(ctx, outputDirectory, run.Time, report.RunID, log); err != nil {
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
	pointerInfo, statErr := os.Stat(filepath.Join(filepath.Dir(report.Directory), "current.json"))
	if statErr != nil {
		return report, statErr
	}
	report.OutputFiles++
	report.OutputBytes += pointerInfo.Size()

	report.DownloadedBytes = transport.bytes.Load()
	report.Elapsed = time.Since(started)
	return report, err
}

func latestRun(ctx context.Context, client *ifs.Client) (ifs.Run, error) {
	now := time.Now().UTC()
	candidate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
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
		candidate = candidate.Add(-24 * time.Hour)
	}
	return ifs.Run{}, errors.New("no complete IFS run found in the lookback window")
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
