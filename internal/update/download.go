package update

import (
	"compress/bzip2"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/providers/aifs"
	"nice-weather-finder/internal/providers/dwd"
)

const (
	maxStageBytes            = int64(6 << 30)
	maxDecompressedFileBytes = int64(128 << 20)
	workers                  = 4
	// Each DWD worker can briefly hold a complete compressed file and its
	// decompressed successor between quota checks. Reserve that in-flight
	// headroom so the advertised 6 GiB limit is a hard bound, not a soft one.
	maxCommittedStageBytes = maxStageBytes - workers*(maxDecompressedFileBytes+dwd.MaxCompressedFileBytes)
)

type stagedFrame struct {
	path       string
	field      pipeline.Field
	step       int
	run        time.Time
	resolution float64
	spacing    float64
}

// stagedForDay bounds decoded memory to one product day. The preceding sample
// is retained so cumulative precipitation and interval fields can be
// normalized at the first product-window boundary.
func stagedForDay(staged []stagedFrame, date time.Time, detailed bool) []stagedFrame {
	start := date.Add(-6 * time.Hour)
	end := date.Add(24 * time.Hour)
	if detailed {
		start = date.Add(3 * time.Hour)
		end = date.Add(21 * time.Hour)
	}
	selected := make([]stagedFrame, 0, len(staged))
	preceding := make(map[pipeline.Field]stagedFrame)
	for _, frame := range staged {
		valid := frame.run.Add(time.Duration(frame.step) * time.Hour)
		if valid.Before(start) {
			prior, ok := preceding[frame.field]
			if !ok || frame.step > prior.step {
				preceding[frame.field] = frame
			}
			continue
		}
		if !valid.Before(start) && !valid.After(end) {
			selected = append(selected, frame)
		}
	}
	result := make([]stagedFrame, 0, len(preceding)+len(selected))
	for _, frame := range preceding {
		result = append(result, frame)
	}
	return append(result, selected...)
}

func stageDWD(ctx context.Context, client *http.Client, run dwd.Run, steps []int, stage string) ([]stagedFrame, error) {
	destination := filepath.Join(stage, "dwd")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return nil, err
	}
	required, err := stageDWDFields(ctx, client, run, steps, destination, stage, dwd.RequiredFields())
	if err != nil {
		return nil, fmt.Errorf("required ICON-EU fields: %w", err)
	}
	result := required
	gust, err := dwd.FieldByName("wind_gust_10m")
	if err != nil {
		return nil, err
	}
	if staged, optionalErr := stageDWDFields(ctx, client, run, steps, destination, stage, []dwd.Field{gust}); optionalErr != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		fmt.Printf("warning: ICON-EU gust omitted because the complete optional series was unavailable: %v\n", optionalErr)
	} else {
		result = append(result, staged...)
	}
	direct, err := dwd.FieldByName("direct_shortwave_down")
	if err != nil {
		return nil, err
	}
	diffuse, err := dwd.FieldByName("diffuse_shortwave_down")
	if err != nil {
		return nil, err
	}
	if staged, optionalErr := stageDWDFields(ctx, client, run, steps, destination, stage, []dwd.Field{direct, diffuse}); optionalErr != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		fmt.Printf("warning: ICON-EU solar omitted because the direct and diffuse series were not both complete: %v\n", optionalErr)
	} else {
		result = append(result, staged...)
	}
	return result, nil
}

func stageDWDFields(ctx context.Context, client *http.Client, run dwd.Run, steps []int, destination, stage string, fields []dwd.Field) ([]stagedFrame, error) {
	type task struct {
		file  dwd.File
		field pipeline.Field
	}
	var tasks []task
	for _, field := range fields {
		mapped, err := dwdPipelineField(field.Name)
		if err != nil {
			return nil, err
		}
		for _, step := range steps {
			file, err := dwd.FileFor(dwd.DefaultBaseURL, run, field, step)
			if err != nil {
				return nil, err
			}
			tasks = append(tasks, task{file: file, field: mapped})
		}
	}
	results := make([]stagedFrame, len(tasks))
	err := runWorkers(ctx, len(tasks), func(workerCtx context.Context, index int) error {
		task := tasks[index]
		base := fmt.Sprintf("%s-%03d.grib2", task.file.Field.Directory, task.file.Step)
		compressed := filepath.Join(destination, base+".bz2")
		plain := filepath.Join(destination, base)
		downloader := dwd.Downloader{Client: client}
		if err := withRetry(workerCtx, func() error { return downloader.FetchCompressed(workerCtx, task.file, compressed, nil) }); err != nil {
			return err
		}
		if err := decompressBzip2(compressed, plain); err != nil {
			return err
		}
		if err := os.Remove(compressed); err != nil {
			return err
		}
		if err := enforceQuota(stage); err != nil {
			return err
		}
		results[index] = stagedFrame{path: plain, field: task.field, step: task.file.Step, run: run.StartedAt, resolution: 7, spacing: .0625}
		return nil
	})
	if err != nil {
		// Each group is atomic. Remove completed siblings as well as any
		// compressed file left by a cancelled worker so failed groups consume no
		// staging quota and cannot be decoded accidentally.
		for _, task := range tasks {
			base := filepath.Join(destination, fmt.Sprintf("%s-%03d.grib2", task.file.Field.Directory, task.file.Step))
			_ = os.Remove(base)
			_ = os.Remove(base + ".bz2")
		}
		return nil, err
	}
	return results, nil
}

func stageAIFS(ctx context.Context, client *aifs.Client, run aifs.Run, steps []int, stage string) ([]stagedFrame, error) {
	destination := filepath.Join(stage, "aifs")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return nil, err
	}
	type task struct {
		step    int
		message aifs.Message
		field   pipeline.Field
	}
	var tasks []task
	for _, step := range steps {
		var messages map[string]aifs.Message
		err := withRetry(ctx, func() error {
			var fetchErr error
			messages, fetchErr = client.FetchIndex(ctx, run, step)
			return fetchErr
		})
		if err != nil {
			return nil, fmt.Errorf("AIFS step %d index: %w", step, err)
		}
		for _, parameter := range aifs.RequiredSurfaceFields() {
			field, err := aifsPipelineField(parameter)
			if err != nil {
				return nil, err
			}
			tasks = append(tasks, task{step: step, message: messages[parameter], field: field})
		}
	}
	results := make([]stagedFrame, len(tasks))
	err := runWorkers(ctx, len(tasks), func(workerCtx context.Context, index int) error {
		task := tasks[index]
		path := filepath.Join(destination, fmt.Sprintf("%s-%03d.grib2", task.message.Parameter, task.step))
		file, err := os.Create(path)
		if err != nil {
			return err
		}
		err = withRetry(workerCtx, func() error {
			if _, seekErr := file.Seek(0, io.SeekStart); seekErr != nil {
				return seekErr
			}
			if truncateErr := file.Truncate(0); truncateErr != nil {
				return truncateErr
			}
			return client.DownloadIndexedMessage(workerCtx, run, task.step, task.message, file)
		})
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if err := enforceQuota(stage); err != nil {
			return err
		}
		results[index] = stagedFrame{path: path, field: task.field, step: task.step, run: run.Time, resolution: 28, spacing: .25}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func decodeFrames(ctx context.Context, decoder *pipeline.Decoder, staged []stagedFrame) (pipeline.Series, error) {
	series := make(pipeline.Series)
	for index, item := range staged {
		frame, err := decoder.Decode(ctx, pipeline.DecodeRequest{
			Path: item.path, Field: item.field, Run: item.run,
			NativeResolutionKM: item.resolution, CellDegrees: item.spacing,
		})
		if err != nil {
			return nil, fmt.Errorf("decode %s step %d: %w", item.field, item.step, err)
		}
		series[item.field] = append(series[item.field], frame)
		if (index+1)%25 == 0 || index+1 == len(staged) {
			fmt.Printf("decoded %d/%d forecast fields\n", index+1, len(staged))
		}
	}
	return series, nil
}

func dwdPipelineField(name string) (pipeline.Field, error) {
	fields := map[string]pipeline.Field{
		"temperature_2m": pipeline.Temperature, "dewpoint_2m": pipeline.DewPoint,
		"total_cloud_cover": pipeline.CloudCover, "total_precipitation": pipeline.Rain,
		"wind_u_10m": pipeline.WindU, "wind_v_10m": pipeline.WindV,
		"wind_gust_10m":         pipeline.Gust,
		"direct_shortwave_down": pipeline.SolarDirect, "diffuse_shortwave_down": pipeline.SolarDiffuse,
	}
	field, ok := fields[name]
	if !ok {
		return "", fmt.Errorf("unsupported required DWD field %q", name)
	}
	return field, nil
}

func aifsPipelineField(parameter string) (pipeline.Field, error) {
	fields := map[string]pipeline.Field{
		"2t": pipeline.Temperature, "2d": pipeline.DewPoint, "tcc": pipeline.CloudCover,
		"tp": pipeline.Rain, "10u": pipeline.WindU, "10v": pipeline.WindV, "ssrd": pipeline.Solar,
	}
	field, ok := fields[parameter]
	if !ok {
		return "", fmt.Errorf("unsupported required AIFS field %q", parameter)
	}
	return field, nil
}

func decompressBzip2(source, destination string) (err error) {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := output.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(destination)
		}
	}()
	written, copyErr := io.Copy(output, io.LimitReader(bzip2.NewReader(input), maxDecompressedFileBytes+1))
	if copyErr != nil {
		return copyErr
	}
	if written > maxDecompressedFileBytes {
		return fmt.Errorf("decompressed DWD field exceeds %d-byte limit", maxDecompressedFileBytes)
	}
	return output.Sync()
}

func runWorkers(parent context.Context, count int, work func(context.Context, int) error) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	jobs := make(chan int)
	errs := make(chan error, 1)
	var group sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for index := range jobs {
				if err := work(ctx, index); err != nil {
					select {
					case errs <- err:
						cancel()
					default:
					}
					return
				}
			}
		}()
	}
send:
	for index := 0; index < count; index++ {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break send
		}
	}
	close(jobs)
	group.Wait()
	select {
	case err := <-errs:
		return err
	default:
		return parent.Err()
	}
}

func enforceQuota(root string) error {
	var size int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		size += info.Size()
		if size > maxCommittedStageBytes {
			return errors.New("weather staging exceeds the 6 GiB disk quota")
		}
		return nil
	})
	return err
}
