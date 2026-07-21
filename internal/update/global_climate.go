package update

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode"

	"nice-weather-finder/internal/climateassembly"
	"nice-weather-finder/internal/climatebuild"
	"nice-weather-finder/internal/climatetiles"
	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/providers/agera5"
)

const (
	maximumGlobalClimateTokenBytes = 16 << 10
	maximumMainPointerBytes        = 64 << 10
	minimumCheckpointFreeBytes     = uint64(4 << 30)
	minimumArtifactFreeBytes       = uint64(1 << 30)
	globalClimateReleaseContract   = "nice-weather-global-climate-release-v1"
)

// GlobalClimateConfig identifies one resumable worldwide AgERA5 build and
// one independently published calendar month. CheckpointRoot may be on an
// external volume, but it must already exist at a clean absolute path.
type GlobalClimateConfig struct {
	ArtifactRoot   string
	CheckpointRoot string
	LandCacheRoot  string
	Period         string
	Month          time.Month
	TokenReader    io.Reader
}

// GlobalClimateSummary is bounded production evidence. It contains no token,
// source object key, URL query or decoded source chunk.
type GlobalClimateSummary struct {
	ReleaseID            string
	Pointer              climatetiles.Pointer
	SourceResponses      int
	SourceResponseBytes  int64
	BlocksReused         int
	BlocksBuilt          int
	BlocksSkipped        int
	CheckpointBytes      int64
	ArtifactBytes        int64
	PointBlocks          int
	ScalarTiles          int
	TemperatureRepairs   int64
	MaximumInversionC    float64
	MainPointerUnchanged bool
	Elapsed              time.Duration
}

type globalClimateDependencies struct {
	now             func() time.Time
	retry           climateRetryFunc
	fetchMetadata   func(context.Context, string) (agera5.Metadata, error)
	sourceEvidence  func(agera5.Metadata) ([sha256.Size]byte, error)
	newSource       func(agera5.Metadata, string) climatebuild.Source
	retrySource     func(climatebuild.Source) climatebuild.Source
	sourceMetrics   func() agera5.ClientMetrics
	landMask        func(context.Context, string, delivery.GridSpec) (pipeline.FloatGrid, error)
	availableBytes  func(string) (uint64, error)
	runBlocks       func(context.Context, string, climatebuild.Source, pipeline.FloatGrid, [sha256.Size]byte, int) (GlobalClimateBlockSummary, error)
	buildMonth      func(context.Context, climateassembly.Config) (climatetiles.Catalog, error)
	publish         func(context.Context, string, string, climatetiles.Builder) (climatetiles.Pointer, error)
	validateCurrent func(string) (climatetiles.Pointer, climatetiles.Catalog, error)
	treeStats       func(string) (int, int64, error)
}

// RunGlobalClimate fetches no bulk source chunks until the bearer token,
// small metadata contract, Natural Earth mask and both destination volumes
// pass preflight. Source chunks remain in memory only; the durable resumable
// boundary is the compact checkpoint set.
func RunGlobalClimate(ctx context.Context, config GlobalClimateConfig) (GlobalClimateSummary, error) {
	client := agera5.NewClient()
	return runGlobalClimate(ctx, config, productionGlobalClimateDependencies(client))
}

func productionGlobalClimateDependencies(client *agera5.Client) globalClimateDependencies {
	return globalClimateDependencies{
		now:            time.Now,
		retry:          withRetry,
		fetchMetadata:  client.FetchMetadata,
		sourceEvidence: globalClimateSourceEvidenceSHA256,
		newSource: func(metadata agera5.Metadata, token string) climatebuild.Source {
			return climatebuild.NewAgERA5Source(metadata, client, token)
		},
		retrySource:   newGlobalClimateRetrySource,
		sourceMetrics: client.Metrics,
		landMask: func(ctx context.Context, cacheRoot string, spec delivery.GridSpec) (pipeline.FloatGrid, error) {
			return NewLandAuthority(cacheRoot, http.DefaultClient, nil).CenterMask(ctx, spec)
		},
		availableBytes:  filesystemAvailableBytes,
		runBlocks:       RunGlobalClimateBlocks,
		buildMonth:      climateassembly.BuildMonth,
		publish:         climatetiles.BuildAndPublish,
		validateCurrent: climatetiles.ValidateCurrent,
		treeStats:       treeStats,
	}
}

func runGlobalClimate(ctx context.Context, config GlobalClimateConfig, dependencies globalClimateDependencies) (summary GlobalClimateSummary, resultErr error) {
	if err := validateGlobalClimateConfig(ctx, config, dependencies); err != nil {
		return summary, err
	}
	started := dependencies.now()
	mainBefore, err := inspectMainPointer(config.ArtifactRoot)
	if err != nil {
		return summary, err
	}
	defer func() {
		mainAfter, err := inspectMainPointer(config.ArtifactRoot)
		if err != nil {
			resultErr = errors.Join(resultErr, err)
			return
		}
		if mainAfter != mainBefore {
			resultErr = errors.Join(resultErr, errors.New("main artifacts/current changed during global climate publication"))
			return
		}
		summary.MainPointerUnchanged = true
	}()

	token, err := readGlobalClimateToken(config.TokenReader)
	if err != nil {
		return summary, err
	}
	if err := ctx.Err(); err != nil {
		return summary, err
	}
	var metadata agera5.Metadata
	err = dependencies.retry(ctx, func() error {
		var fetchErr error
		metadata, fetchErr = dependencies.fetchMetadata(ctx, token)
		return fetchErr
	})
	if err != nil {
		return summary, secretSafeGlobalClimateError("fetch and validate AgERA5 metadata", err, token)
	}
	sourceEvidence, err := dependencies.sourceEvidence(metadata)
	if err != nil {
		return summary, err
	}
	if err := ctx.Err(); err != nil {
		return summary, err
	}
	worldLand, err := dependencies.landMask(ctx, config.LandCacheRoot, climatebuild.WorldwideGridSpec())
	if err != nil {
		return summary, fmt.Errorf("prepare Natural Earth worldwide centre mask: %w", err)
	}
	if err := preflightGlobalClimateDisk(config, dependencies.availableBytes); err != nil {
		return summary, err
	}
	if err := ctx.Err(); err != nil {
		return summary, err
	}
	source := dependencies.retrySource(dependencies.newSource(metadata, token))
	blocks, err := dependencies.runBlocks(
		ctx,
		config.CheckpointRoot,
		source,
		worldLand,
		sourceEvidence,
		DefaultGlobalClimateWorkers,
	)
	if err != nil {
		return summary, secretSafeGlobalClimateError("build worldwide climate checkpoints", err, token)
	}
	summary.BlocksReused = blocks.Reused
	summary.BlocksBuilt = blocks.Built
	summary.BlocksSkipped = blocks.WaterSkipped
	summary.CheckpointBytes = blocks.CheckpointBytes
	summary.TemperatureRepairs = blocks.TemperatureRepairs
	summary.MaximumInversionC = blocks.MaximumInversionC

	releaseID := globalClimateReleaseID(config.Period, config.Month, sourceEvidence)
	loader := globalClimateCheckpointLoader(config.CheckpointRoot)
	if err := ctx.Err(); err != nil {
		return summary, err
	}
	pointer, err := dependencies.publish(ctx, config.ArtifactRoot, releaseID, func(buildCtx context.Context, stage climatetiles.Stage) (climatetiles.Catalog, error) {
		if err := buildCtx.Err(); err != nil {
			return climatetiles.Catalog{}, err
		}
		return dependencies.buildMonth(buildCtx, climateassembly.Config{
			Stage:          stage,
			Period:         config.Period,
			Month:          config.Month,
			Checkpoints:    append([]climatebuild.CheckpointIdentity(nil), blocks.Completed...),
			LoadCheckpoint: loader,
		})
	})
	if err != nil {
		return summary, fmt.Errorf("build and publish worldwide climate month: %w", err)
	}
	validatedPointer, catalog, err := dependencies.validateCurrent(config.ArtifactRoot)
	if err != nil {
		return summary, fmt.Errorf("validate current worldwide climate month: %w", err)
	}
	if validatedPointer != pointer || catalog.ReleaseID != releaseID || catalog.Period != config.Period {
		return summary, errors.New("published worldwide climate pointer does not identify the requested release")
	}
	_, artifactBytes, err := dependencies.treeStats(filepath.Join(config.ArtifactRoot, "climate", "releases", releaseID))
	if err != nil {
		return summary, fmt.Errorf("measure worldwide climate release: %w", err)
	}
	metrics := dependencies.sourceMetrics()
	summary.ReleaseID = releaseID
	summary.Pointer = pointer
	summary.SourceResponses = metrics.Responses
	summary.SourceResponseBytes = metrics.FetchedBytes
	summary.ArtifactBytes = artifactBytes
	summary.PointBlocks = len(catalog.PointBlocks)
	summary.ScalarTiles = len(catalog.ScalarTiles)
	summary.Elapsed = dependencies.now().Sub(started)
	if summary.Elapsed < 0 {
		summary.Elapsed = 0
	}
	return summary, nil
}

func validateGlobalClimateConfig(ctx context.Context, config GlobalClimateConfig, dependencies globalClimateDependencies) error {
	if ctx == nil {
		return errors.New("nil global climate context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if config.ArtifactRoot == "" || config.CheckpointRoot == "" || config.LandCacheRoot == "" || config.TokenReader == nil {
		return errors.New("global climate artifact, checkpoint and land-cache roots and token reader are required")
	}
	if config.Month < time.January || config.Month > time.December || config.Period != fmt.Sprintf("month-%02d", config.Month) {
		return errors.New("global climate period and month are not one canonical month identity")
	}
	if !filepath.IsAbs(config.CheckpointRoot) || filepath.Clean(config.CheckpointRoot) != config.CheckpointRoot || config.CheckpointRoot == string(filepath.Separator) {
		return errors.New("global climate checkpoint root must be a clean absolute directory")
	}
	for name, root := range map[string]string{"artifact": config.ArtifactRoot, "checkpoint": config.CheckpointRoot} {
		info, err := os.Lstat(root)
		if err != nil {
			return fmt.Errorf("stat global climate %s root: %w", name, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("global climate %s root must be an existing real directory", name)
		}
	}
	if dependencies.now == nil || dependencies.retry == nil || dependencies.fetchMetadata == nil || dependencies.sourceEvidence == nil || dependencies.newSource == nil || dependencies.retrySource == nil || dependencies.sourceMetrics == nil || dependencies.landMask == nil || dependencies.availableBytes == nil || dependencies.runBlocks == nil || dependencies.buildMonth == nil || dependencies.publish == nil || dependencies.validateCurrent == nil || dependencies.treeStats == nil {
		return errors.New("global climate dependencies are incomplete")
	}
	return nil
}

func readGlobalClimateToken(reader io.Reader) (string, error) {
	if reader == nil {
		return "", errors.New("AgERA5 bearer token reader is required")
	}
	payload, err := io.ReadAll(io.LimitReader(reader, maximumGlobalClimateTokenBytes+1))
	if err != nil {
		return "", errors.New("read AgERA5 bearer token")
	}
	if len(payload) > maximumGlobalClimateTokenBytes {
		return "", fmt.Errorf("AgERA5 bearer token exceeds %d-byte limit", maximumGlobalClimateTokenBytes)
	}
	token := strings.TrimSpace(string(payload))
	if token == "" {
		return "", errors.New("AgERA5 bearer token is empty")
	}
	if strings.IndexFunc(token, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return "", errors.New("AgERA5 bearer token contains whitespace or control characters")
	}
	return token, nil
}

func secretSafeGlobalClimateError(prefix string, err error, token string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	message := err.Error()
	if token != "" {
		message = strings.ReplaceAll(message, token, "[redacted]")
	}
	return errors.New(prefix + ": " + message)
}

type globalClimateSourcePlanEntry struct {
	Key       string
	Field     agera5.Field
	FirstDate time.Time
	LastDate  time.Time
	Block     agera5.BlockCoordinate
}

// globalClimateSourceEvidenceSHA256 deliberately excludes the consolidated
// metadata object identity and its append-only time length. Planning the fixed
// reference window both proves that metadata covers 2025-12-31 and yields the
// stable source objects that can affect checkpoints.
func globalClimateSourceEvidenceSHA256(metadata agera5.Metadata) ([sha256.Size]byte, error) {
	firstDate := time.Date(climatebuild.CheckpointFirstYear, time.January, 1, 0, 0, 0, 0, time.UTC)
	lastDate := time.Date(climatebuild.CheckpointLastYear, time.December, 31, 0, 0, 0, 0, time.UTC)
	canonicalBlock := agera5.BlockCoordinate{Latitude: 79, Longitude: 54}
	references, err := metadata.PlanChunks(firstDate, lastDate, canonicalBlock)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("plan fixed AgERA5 checkpoint evidence: %w", err)
	}
	plan := make([]globalClimateSourcePlanEntry, len(references))
	for index, reference := range references {
		plan[index] = globalClimateSourcePlanEntry{
			Key: reference.Key(), Field: reference.Field(), FirstDate: reference.FirstDate(), LastDate: reference.LastDate(), Block: reference.Block(),
		}
	}
	return digestGlobalClimateSourcePlan(
		agera5.DefaultRoot,
		agera5.RequiredFields(),
		firstDate,
		lastDate,
		climatebuild.CheckpointDerivationContract,
		plan,
	)
}

func digestGlobalClimateSourcePlan(root string, fields []agera5.Field, firstDate, lastDate time.Time, derivation string, plan []globalClimateSourcePlanEntry) ([sha256.Size]byte, error) {
	if root == "" || !globalClimateUTCDate(firstDate) || !globalClimateUTCDate(lastDate) || lastDate.Before(firstDate) || derivation == "" || len(fields) == 0 || len(plan) == 0 {
		return [sha256.Size]byte{}, errors.New("global climate source contract is incomplete")
	}
	canonicalFields := make([]string, len(fields))
	for index, field := range fields {
		if field == "" {
			return [sha256.Size]byte{}, errors.New("global climate source contract contains an empty field")
		}
		canonicalFields[index] = string(field)
	}
	slices.Sort(canonicalFields)
	for index := 1; index < len(canonicalFields); index++ {
		if canonicalFields[index] == canonicalFields[index-1] {
			return [sha256.Size]byte{}, errors.New("global climate source contract contains duplicate fields")
		}
	}
	digest := sha256.New()
	writeDigestString(digest, "nice-weather-global-climate-source-v1")
	writeDigestString(digest, root)
	writeDigestString(digest, firstDate.Format(time.DateOnly))
	writeDigestString(digest, lastDate.Format(time.DateOnly))
	for _, field := range canonicalFields {
		writeDigestString(digest, field)
	}
	writeDigestString(digest, derivation)
	canonicalPlan := append([]globalClimateSourcePlanEntry(nil), plan...)
	sort.Slice(canonicalPlan, func(i, j int) bool {
		a, b := canonicalPlan[i], canonicalPlan[j]
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		if a.FirstDate != b.FirstDate {
			return a.FirstDate.Before(b.FirstDate)
		}
		return a.LastDate.Before(b.LastDate)
	})
	fieldCounts := make(map[agera5.Field]int, len(canonicalFields))
	var encoded [8]byte
	for index, entry := range canonicalPlan {
		if entry.Key == "" || !globalClimateUTCDate(entry.FirstDate) || !globalClimateUTCDate(entry.LastDate) || entry.LastDate.Before(entry.FirstDate) || entry.FirstDate.Before(firstDate) || entry.LastDate.After(lastDate) || !slices.Contains(fields, entry.Field) {
			return [sha256.Size]byte{}, fmt.Errorf("global climate source plan entry %d is invalid", index)
		}
		if index > 0 && entry.Key == canonicalPlan[index-1].Key {
			return [sha256.Size]byte{}, fmt.Errorf("global climate source plan contains duplicate key %q", entry.Key)
		}
		fieldCounts[entry.Field]++
		writeDigestString(digest, entry.Key)
		writeDigestString(digest, string(entry.Field))
		writeDigestString(digest, entry.FirstDate.Format(time.DateOnly))
		writeDigestString(digest, entry.LastDate.Format(time.DateOnly))
		binary.LittleEndian.PutUint32(encoded[0:4], uint32(entry.Block.Latitude))
		binary.LittleEndian.PutUint32(encoded[4:8], uint32(entry.Block.Longitude))
		_, _ = digest.Write(encoded[:])
	}
	wantCount := 0
	for _, field := range fields {
		count := fieldCounts[field]
		if count == 0 {
			return [sha256.Size]byte{}, fmt.Errorf("global climate source plan omits required field %q", field)
		}
		if wantCount == 0 {
			wantCount = count
		} else if count != wantCount {
			return [sha256.Size]byte{}, errors.New("global climate source plan is incomplete across required fields")
		}
	}
	var result [sha256.Size]byte
	copy(result[:], digest.Sum(nil))
	return result, nil
}

func globalClimateUTCDate(value time.Time) bool {
	return value.Location() == time.UTC && value.Hour() == 0 && value.Minute() == 0 && value.Second() == 0 && value.Nanosecond() == 0
}

func writeDigestString(writer io.Writer, value string) {
	var length [8]byte
	binary.LittleEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = writer.Write(length[:])
	_, _ = io.WriteString(writer, value)
}

func globalClimateReleaseID(period string, month time.Month, sourceEvidence [sha256.Size]byte) string {
	digest := sha256.New()
	writeDigestString(digest, globalClimateReleaseContract)
	writeDigestString(digest, period)
	_, _ = digest.Write([]byte{byte(month)})
	_, _ = digest.Write(sourceEvidence[:])
	return fmt.Sprintf("agera5-%s-%s", period, hex.EncodeToString(digest.Sum(nil))[:20])
}

func preflightGlobalClimateDisk(config GlobalClimateConfig, available func(string) (uint64, error)) error {
	checks := []struct {
		name    string
		root    string
		minimum uint64
	}{
		{"checkpoint", config.CheckpointRoot, minimumCheckpointFreeBytes},
		{"artifact", config.ArtifactRoot, minimumArtifactFreeBytes},
	}
	for _, check := range checks {
		bytes, err := available(check.root)
		if err != nil {
			return fmt.Errorf("measure global climate %s volume: %w", check.name, err)
		}
		if bytes < check.minimum {
			return fmt.Errorf("global climate %s volume has %d bytes available; need at least %d", check.name, bytes, check.minimum)
		}
	}
	return nil
}

func filesystemAvailableBytes(path string) (uint64, error) {
	var stats syscall.Statfs_t
	if err := syscall.Statfs(path, &stats); err != nil {
		return 0, err
	}
	if stats.Bavail < 0 || stats.Bsize <= 0 {
		return 0, errors.New("filesystem returned invalid available-block metadata")
	}
	return uint64(stats.Bavail) * uint64(stats.Bsize), nil
}

func globalClimateCheckpointLoader(root string) climateassembly.CheckpointLoader {
	return func(ctx context.Context, identity climatebuild.CheckpointIdentity) (climatebuild.Checkpoint, error) {
		if err := ctx.Err(); err != nil {
			return climatebuild.Checkpoint{}, err
		}
		path := globalClimateCheckpointPath(root, identity.Block)
		checkpoint, found, err := climatebuild.LoadCheckpoint(path, identity)
		if err != nil {
			return climatebuild.Checkpoint{}, err
		}
		if !found {
			return climatebuild.Checkpoint{}, fmt.Errorf("exact climate checkpoint block %+v is missing", identity.Block)
		}
		return checkpoint, nil
	}
}

type mainPointerSnapshot struct {
	exists     bool
	mode       os.FileMode
	size       int64
	modifiedNS int64
	target     string
	content    [sha256.Size]byte
}

func inspectMainPointer(artifactRoot string) (mainPointerSnapshot, error) {
	path := filepath.Join(artifactRoot, "current")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return mainPointerSnapshot{}, nil
	}
	if err != nil {
		return mainPointerSnapshot{}, fmt.Errorf("inspect main artifacts/current: %w", err)
	}
	snapshot := mainPointerSnapshot{exists: true, mode: info.Mode(), size: info.Size(), modifiedNS: info.ModTime().UnixNano()}
	if info.Mode()&os.ModeSymlink != 0 {
		snapshot.target, err = os.Readlink(path)
		if err != nil {
			return mainPointerSnapshot{}, fmt.Errorf("read main artifacts/current link: %w", err)
		}
	} else if info.Mode().IsRegular() {
		file, openErr := os.Open(path)
		if openErr != nil {
			return mainPointerSnapshot{}, fmt.Errorf("open main artifacts/current: %w", openErr)
		}
		payload, readErr := io.ReadAll(io.LimitReader(file, maximumMainPointerBytes+1))
		closeErr := file.Close()
		if readErr != nil {
			return mainPointerSnapshot{}, fmt.Errorf("read main artifacts/current: %w", readErr)
		}
		if closeErr != nil {
			return mainPointerSnapshot{}, fmt.Errorf("close main artifacts/current: %w", closeErr)
		}
		if len(payload) > maximumMainPointerBytes {
			return mainPointerSnapshot{}, fmt.Errorf("main artifacts/current exceeds %d-byte limit", maximumMainPointerBytes)
		}
		snapshot.content = sha256.Sum256(payload)
	}
	return snapshot, nil
}
