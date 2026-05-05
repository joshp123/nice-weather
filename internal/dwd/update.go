package dwd

import (
	"compress/bzip2"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Run struct {
	ID        string
	Hour      string
	StartedAt time.Time
}

const latestEuropeTimezoneOffsetWestHours = 1

func CandidateRuns(now time.Time) []Run {
	now = now.UTC()
	rounded := time.Date(now.Year(), now.Month(), now.Day(), now.Hour()/6*6, 0, 0, 0, time.UTC)
	runs := make([]Run, 0, 16)
	for i := 0; i < 16; i++ {
		startedAt := rounded.Add(-time.Duration(i*6) * time.Hour)
		runs = append(runs, Run{
			ID:        startedAt.Format("2006010215"),
			Hour:      startedAt.Format("15"),
			StartedAt: startedAt,
		})
	}
	return runs
}

func LatestCompleteRunForLocalDays(ctx context.Context, now time.Time, steps []int, days int) (Run, error) {
	if days < 1 {
		return Run{}, fmt.Errorf("forecast days must be positive")
	}
	if len(steps) == 0 {
		return Run{}, fmt.Errorf("cannot discover DWD run without required steps")
	}
	maxStep := steps[len(steps)-1]
	for _, run := range CandidateRuns(now) {
		if !RunCoversLocalDays(run, days, maxStep) {
			continue
		}
		complete := true
		for _, field := range RequiredFolders {
			url, err := FileURL(run.Hour, field, run.ID, maxStep)
			if err != nil {
				return Run{}, err
			}
			ok, err := remoteFileExists(ctx, url)
			if err != nil {
				return Run{}, err
			}
			if !ok {
				complete = false
				break
			}
		}
		if complete {
			return run, nil
		}
	}
	return Run{}, fmt.Errorf("no complete DWD ICON-EU run can cover %d full local forecast dates", days)
}

func RunCoversLocalDays(run Run, days, maxStep int) bool {
	horizons := FullLocalHorizonDates(run.StartedAt, days)
	if len(horizons) == 0 {
		return false
	}
	lastDate, err := time.Parse(time.DateOnly, horizons[len(horizons)-1].LocalDate)
	if err != nil {
		return false
	}
	latestEuropeDaytimeEndUTC := time.Date(lastDate.Year(), lastDate.Month(), lastDate.Day(), WindowEndHour+latestEuropeTimezoneOffsetWestHours, 0, 0, 0, time.UTC)
	requiredStep := latestEuropeDaytimeEndUTC.Sub(run.StartedAt.UTC()).Hours()
	return requiredStep >= 0 && requiredStep <= float64(maxStep)
}

func DownloadRun(ctx context.Context, run Run, steps []int, cacheRoot string) (string, error) {
	runDir := filepath.Join(cacheRoot, run.ID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return "", err
	}

	jobs := make(chan downloadJob)
	errs := make(chan error, 1)
	var wg sync.WaitGroup
	workerCount := 6
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				if err := downloadField(ctx, run, runDir, job.field, job.step); err != nil {
					select {
					case errs <- err:
					default:
					}
					return
				}
			}
		}()
	}

	for _, step := range steps {
		for _, field := range RequiredFolders {
			select {
			case err := <-errs:
				close(jobs)
				wg.Wait()
				return "", err
			case jobs <- downloadJob{field: field, step: step}:
			}
		}
	}
	close(jobs)
	wg.Wait()

	select {
	case err := <-errs:
		return "", err
	default:
		return runDir, nil
	}
}

type downloadJob struct {
	field string
	step  int
}

func downloadField(ctx context.Context, run Run, dir, field string, step int) error {
	url, err := FileURL(run.Hour, field, run.ID, step)
	if err != nil {
		return err
	}
	gribName := gribNames[field]
	base := fmt.Sprintf("icon-eu_europe_regular-lat-lon_single-level_%s_%03d_%s.grib2", run.ID, step, gribName)
	gribPath := filepath.Join(dir, base)
	if fileExists(gribPath) {
		_ = os.Remove(gribPath + ".bz2")
		return nil
	}
	bz2Path := gribPath + ".bz2"
	if !fileExists(bz2Path) {
		if err := downloadURL(ctx, url, bz2Path); err != nil {
			return err
		}
	}
	if err := decompressBzip2(bz2Path, gribPath); err != nil {
		return err
	}
	return os.Remove(bz2Path)
}

func PruneCachedRuns(cacheRoot string, keep int) error {
	if keep < 1 {
		return fmt.Errorf("must keep at least one cached DWD run")
	}
	entries, err := os.ReadDir(cacheRoot)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	runs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			runs = append(runs, entry.Name())
		}
	}
	sort.Strings(runs)
	for len(runs) > keep {
		oldest := filepath.Join(cacheRoot, runs[0])
		if err := os.RemoveAll(oldest); err != nil {
			return err
		}
		runs = runs[1:]
	}
	for _, run := range runs {
		if err := removeCompressedCopies(filepath.Join(cacheRoot, run)); err != nil {
			return err
		}
	}
	return nil
}

func removeCompressedCopies(runDir string) error {
	return filepath.WalkDir(runDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".bz2" {
			return nil
		}
		if fileExists(path[:len(path)-len(".bz2")]) {
			return os.Remove(path)
		}
		return nil
	})
}

func remoteFileExists(ctx context.Context, url string) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return false, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		return true, nil
	}
	if response.StatusCode == http.StatusNotFound {
		return false, nil
	}
	return false, fmt.Errorf("HEAD %s returned %s", url, response.Status)
}

func downloadURL(ctx context.Context, url, path string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s returned %s", url, response.Status)
	}

	tmp := path + ".tmp"
	file, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, response.Body); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func decompressBzip2(src, dst string) error {
	input, err := os.Open(src)
	if err != nil {
		return err
	}
	defer input.Close()

	tmp := dst + ".tmp"
	output, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, bzip2.NewReader(input)); err != nil {
		output.Close()
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
