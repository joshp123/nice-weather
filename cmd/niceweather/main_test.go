package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"nice-weather-finder/internal/era5climateproduct"
	"nice-weather-finder/internal/era5publication"
)

func TestUsageListsOnlyActiveArtifactCommands(t *testing.T) {
	var output bytes.Buffer
	writeUsage(&output)

	for _, command := range []string{"forecast-world", "era5-climate-year", "era5-publication-authority", "places", "best-weather", "serve"} {
		if !strings.Contains(output.String(), command) {
			t.Errorf("usage omits active command %q: %s", command, output.String())
		}
	}
	for _, retired := range []string{"fixture", "update", "agera5-canary", "climate-world"} {
		if strings.Contains(output.String(), retired) {
			t.Errorf("usage still advertises retired command %q: %s", retired, output.String())
		}
	}
}

func TestERA5PublicationAuthorityCommandPassesExactInputsAndReportsDigests(t *testing.T) {
	var stdout, stderr bytes.Buffer
	called := 0
	err := runERA5PublicationAuthorityCommand(context.Background(), []string{
		"--plan", "/trusted/world-2025-11.json.gz",
		"--plan-sha256", strings.Repeat("a", 64),
		"--land-cache", "/large-disk/land-cache",
		"--out", "/large-disk/authority",
	}, &stdout, &stderr, func(_ context.Context, config era5publication.Config) (era5publication.Summary, error) {
		called++
		if config != (era5publication.Config{
			PlanPath: "/trusted/world-2025-11.json.gz", PlanSHA256: strings.Repeat("a", 64),
			LandCacheRoot: "/large-disk/land-cache", OutputRoot: "/large-disk/authority",
		}) {
			t.Fatalf("config = %+v", config)
		}
		return era5publication.Summary{
			OutputRoot: "/large-disk/authority", CentreLandCells: 364_578,
			DisplayMaskSHA256:          strings.Repeat("b", 64),
			PublicationAuthoritySHA256: strings.Repeat("c", 64),
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 || stderr.Len() != 0 || strings.Count(strings.TrimSpace(stdout.String()), "\n") != 0 {
		t.Fatalf("called=%d stdout=%q stderr=%q", called, stdout.String(), stderr.String())
	}
	for _, want := range []string{"built", "364578 land centres", strings.Repeat("b", 64), strings.Repeat("c", 64), "/large-disk/authority"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("summary lacks %q: %s", want, stdout.String())
		}
	}
}

func TestERA5PublicationAuthorityCommandRejectsMissingAndPositionalInputs(t *testing.T) {
	valid := []string{
		"--plan", "/trusted/plan.json.gz", "--plan-sha256", strings.Repeat("a", 64),
		"--land-cache", "/large-disk/cache", "--out", "/large-disk/authority",
	}
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"missing plan", valid[2:], "--plan is required"},
		{"positional", append(append([]string(nil), valid...), "extra"), "accepts no positional arguments"},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			var stdout, stderr bytes.Buffer
			err := runERA5PublicationAuthorityCommand(context.Background(), test.args, &stdout, &stderr,
				func(context.Context, era5publication.Config) (era5publication.Summary, error) {
					called = true
					return era5publication.Summary{}, nil
				})
			if err == nil || !strings.Contains(err.Error(), test.want) || called {
				t.Fatalf("called=%t error=%v; want %q", called, err, test.want)
			}
		})
	}
}

func TestVerifyClimateCommandUsesERA5ReadOnlyGate(t *testing.T) {
	var stdout, stderr bytes.Buffer
	called := 0
	verify := func(root string) (era5climateproduct.PublicationVerification, error) {
		called++
		if root != "/verified/artifacts" {
			t.Fatalf("verification input = %q", root)
		}
		return era5climateproduct.PublicationVerification{
			Pointer: era5climateproduct.Pointer{IndexID: "era5-2006-2025"},
			Index: era5climateproduct.Index{
				ReferencePeriod: era5climateproduct.ReferencePeriod{FirstYear: 2006, LastYear: 2025},
			},
		}, nil
	}
	if err := runVerifyClimateCommand([]string{"--out", "/verified/artifacts"}, &stdout, &stderr, verify); err != nil {
		t.Fatal(err)
	}
	if called != 1 || stderr.Len() != 0 {
		t.Fatalf("called=%d stderr=%q", called, stderr.String())
	}
	for _, want := range []string{"Climate publication valid: 12 months, 2006-2025, index era5-2006-2025."} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout does not contain %q:\n%s", want, stdout.String())
		}
	}
}

func TestVerifyClimateCommandRejectsArgumentsAndPropagatesFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := runVerifyClimateCommand([]string{"unexpected"}, &stdout, &stderr, func(string) (era5climateproduct.PublicationVerification, error) {
		t.Fatal("verification ran after an invalid positional argument")
		return era5climateproduct.PublicationVerification{}, nil
	}); err == nil || !strings.Contains(err.Error(), "accepts no positional arguments") {
		t.Fatalf("positional argument error = %v", err)
	}
	want := errors.New("publication invalid")
	if err := runVerifyClimateCommand(nil, &stdout, &stderr, func(string) (era5climateproduct.PublicationVerification, error) {
		return era5climateproduct.PublicationVerification{}, want
	}); !errors.Is(err, want) {
		t.Fatalf("verification error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("printed success before validation: %q", stdout.String())
	}
}
