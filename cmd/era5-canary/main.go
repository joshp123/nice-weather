package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"nice-weather-finder/internal/era5canary"
)

func main() {
	inputPath := flag.String("input", "", "absolute path to the bounded ERA5 extractor output")
	outputPath := flag.String("out", "", "absolute path for the evaluated JSON report")
	flag.Parse()
	if flag.NArg() != 0 || !filepath.IsAbs(*inputPath) || !filepath.IsAbs(*outputPath) || filepath.Clean(*inputPath) != *inputPath || filepath.Clean(*outputPath) != *outputPath {
		fmt.Fprintln(os.Stderr, "era5-canary: --input and --out must be clean absolute paths; positional arguments are not accepted")
		os.Exit(2)
	}
	input, err := os.Open(*inputPath)
	if err != nil {
		fail(err)
	}
	decoded, err := era5canary.Decode(input)
	closeErr := input.Close()
	if err != nil {
		fail(err)
	}
	if closeErr != nil {
		fail(closeErr)
	}
	report, err := era5canary.Evaluate(decoded)
	if err != nil {
		fail(err)
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fail(err)
	}
	encoded = append(encoded, '\n')
	temporary := *outputPath + ".tmp"
	if err := os.WriteFile(temporary, encoded, 0o644); err != nil {
		fail(err)
	}
	if err := os.Rename(temporary, *outputPath); err != nil {
		fail(err)
	}
	fmt.Printf("ERA5 canary: %d cells, %d scored days, %d HTTP responses, %d bytes, %d ms; report %s\n", len(report.Cells), len(report.Days), report.Retrieval.RequestCount, report.Retrieval.ResponseBodyBytes, report.Retrieval.WallTimeMilliseconds, *outputPath)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "era5-canary:", err)
	os.Exit(1)
}
