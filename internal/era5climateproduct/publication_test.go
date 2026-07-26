package era5climateproduct

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nice-weather-finder/internal/providers/era5hourly"
)

func TestPublicationAppendsCanonicalMonthsAndRetriesExactReference(t *testing.T) {
	artifactRoot := t.TempDir()
	climateRoot := filepath.Join(artifactRoot, "climate")
	if err := os.Mkdir(climateRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	cell := era5hourly.Cell{Latitude: 0, Longitude: 0}
	november := installPublicationMonth(t, climateRoot, 11, "era5-month-11-a", cell, 71)
	first, err := PublishValidatedMonth(context.Background(), climateRoot, november)
	if err != nil {
		t.Fatal(err)
	}
	if first.Index.State != IndexStatePreview || len(first.Index.Months) != 1 || first.Index.Months[0].Period != "month-11" {
		t.Fatalf("first publication = %+v", first)
	}
	pointerBefore := readTestFile(t, filepath.Join(climateRoot, "current.json"))
	retry, err := PublishValidatedMonth(context.Background(), climateRoot, november)
	if err != nil || retry.Pointer != first.Pointer || !sameIndexForTest(retry.Index, first.Index) || !bytes.Equal(pointerBefore, readTestFile(t, filepath.Join(climateRoot, "current.json"))) {
		t.Fatalf("exact retry = %+v, %v", retry, err)
	}

	january := installPublicationMonth(t, climateRoot, 1, "era5-month-01", cell, 62)
	second, err := PublishValidatedMonth(context.Background(), climateRoot, january)
	if err != nil {
		t.Fatal(err)
	}
	if second.Index.State != IndexStatePreview || len(second.Index.Months) != 2 ||
		second.Index.Months[0].Period != "month-01" || second.Index.Months[1].Period != "month-11" {
		t.Fatalf("ordered publication = %+v", second.Index.Months)
	}
	if second.Pointer.IndexID != january.ReleaseID || second.Pointer == first.Pointer {
		t.Fatalf("pointer did not move to the new immutable index: first=%+v second=%+v", first.Pointer, second.Pointer)
	}
	visiblePointer, visibleIndex, err := ValidateCurrentIndex(climateRoot)
	if err != nil || visiblePointer != second.Pointer || !sameIndexForTest(visibleIndex, second.Index) {
		t.Fatalf("visible publication = %+v %+v, %v", visiblePointer, visibleIndex, err)
	}

	replacement := installPublicationMonth(t, climateRoot, 11, "era5-month-11-b", cell, 72)
	beforeReplacement := readTestFile(t, filepath.Join(climateRoot, "current.json"))
	if _, err := PublishValidatedMonth(context.Background(), climateRoot, replacement); err == nil || !strings.Contains(err.Error(), "replace an existing") {
		t.Fatalf("replacement error = %v", err)
	}
	if !bytes.Equal(beforeReplacement, readTestFile(t, filepath.Join(climateRoot, "current.json"))) {
		t.Fatal("failed replacement changed current.json")
	}
}

func TestPublicationKeepsPointerWhenIndexInstallFailsAndRejectsCorruption(t *testing.T) {
	climateRoot := t.TempDir()
	cell := era5hourly.Cell{Latitude: 0, Longitude: 0}
	firstCatalog := installPublicationMonth(t, climateRoot, 1, "era5-month-01", cell, 61)
	first, err := PublishValidatedMonth(context.Background(), climateRoot, firstCatalog)
	if err != nil {
		t.Fatal(err)
	}
	pointerBefore := readTestFile(t, filepath.Join(climateRoot, "current.json"))

	secondCatalog := installPublicationMonth(t, climateRoot, 2, "era5-month-02", cell, 62)
	collision := filepath.Join(climateRoot, "indexes", secondCatalog.ReleaseID+".json")
	if err := os.WriteFile(collision, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishValidatedMonth(context.Background(), climateRoot, secondCatalog); err == nil || !strings.Contains(err.Error(), "different content") {
		t.Fatalf("index collision error = %v", err)
	}
	if !bytes.Equal(pointerBefore, readTestFile(t, filepath.Join(climateRoot, "current.json"))) {
		t.Fatal("index collision changed current.json before the index was durable")
	}
	if visible, _, err := ValidateCurrentIndex(climateRoot); err != nil || visible != first.Pointer {
		t.Fatalf("first pointer not retained: %+v, %v", visible, err)
	}

	if err := os.WriteFile(filepath.Join(climateRoot, "current.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishValidatedMonth(context.Background(), climateRoot, secondCatalog); err == nil || !strings.Contains(err.Error(), "pointer is invalid") {
		t.Fatalf("corrupt pointer error = %v", err)
	}
}

func TestPublicationRejectsMixedDisplayAuthorityAndChangedCatalog(t *testing.T) {
	climateRoot := t.TempDir()
	firstCell := era5hourly.Cell{Latitude: 0, Longitude: 0}
	firstCatalog := installPublicationMonth(t, climateRoot, 1, "era5-month-01", firstCell, 61)
	publication, err := PublishValidatedMonth(context.Background(), climateRoot, firstCatalog)
	if err != nil {
		t.Fatal(err)
	}
	pointerBefore := readTestFile(t, filepath.Join(climateRoot, "current.json"))

	otherCell := era5hourly.Cell{Latitude: 0, Longitude: 0.25}
	mixed := installPublicationMonth(t, climateRoot, 2, "era5-month-02-mixed", otherCell, 62)
	if _, err := PublishValidatedMonth(context.Background(), climateRoot, mixed); err == nil || !strings.Contains(err.Error(), "different source or display authority") {
		t.Fatalf("mixed authority error = %v", err)
	}
	if !bytes.Equal(pointerBefore, readTestFile(t, filepath.Join(climateRoot, "current.json"))) {
		t.Fatal("mixed authority changed current.json")
	}

	catalogPath := filepath.Join(climateRoot, "releases", firstCatalog.ReleaseID, CatalogFilename)
	catalogBody := readTestFile(t, catalogPath)
	if err := os.WriteFile(catalogPath, append(catalogBody, ' '), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ValidateCurrentIndex(climateRoot); err == nil || !strings.Contains(err.Error(), "reference changed") {
		t.Fatalf("changed catalog error = %v", err)
	}
	if publication.Pointer.IndexID == "" {
		t.Fatal("fixture publication was not visible before corruption")
	}
}

func TestPublicationCompletesTwelveMonthsAndERA5Verification(t *testing.T) {
	artifactRoot := t.TempDir()
	climateRoot := filepath.Join(artifactRoot, "climate")
	if err := os.Mkdir(climateRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	cell := era5hourly.Cell{Latitude: 0, Longitude: 0}
	var publication Publication
	for month := 1; month <= 12; month++ {
		catalog := installPublicationMonth(t, climateRoot, month, fmt.Sprintf("era5-month-%02d", month), cell, uint8(50+month))
		var err error
		publication, err = PublishValidatedMonth(context.Background(), climateRoot, catalog)
		if err != nil {
			t.Fatalf("publish month %d: %v", month, err)
		}
	}
	if publication.Index.State != IndexStateComplete || len(publication.Index.Months) != 12 {
		t.Fatalf("complete publication = %+v", publication.Index)
	}
	verification, err := VerifyCompletePublication(artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	if verification.Pointer != publication.Pointer || len(verification.Catalogs) != 12 {
		t.Fatalf("verification = %+v", verification)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(workingDirectory) })
	if err := os.Chdir(filepath.Dir(artifactRoot)); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyCompletePublication(filepath.Base(artifactRoot)); err != nil {
		t.Fatalf("relative artifact root: %v", err)
	}
	if err := os.Chdir(workingDirectory); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishValidatedMonth(context.Background(), climateRoot, verification.Catalogs[0]); err != nil {
		t.Fatalf("complete exact retry is not idempotent: %v", err)
	}
	extra := installPublicationMonth(t, climateRoot, 1, "era5-month-01-other", cell, 88)
	if _, err := PublishValidatedMonth(context.Background(), climateRoot, extra); err == nil {
		t.Fatal("complete publication accepted a changed month")
	}
}

func TestMonthReferenceRegressionRules(t *testing.T) {
	a := MonthReference{Period: "month-01", ReleaseID: "a", CatalogSHA256: strings.Repeat("a", 64)}
	b := MonthReference{Period: "month-02", ReleaseID: "b", CatalogSHA256: strings.Repeat("b", 64)}
	changed := a
	changed.CatalogSHA256 = strings.Repeat("c", 64)
	if monthReferenceSuperset([]MonthReference{a}, []MonthReference{a, b}) {
		t.Fatal("accepted a shrinking month set")
	}
	if monthReferenceSuperset([]MonthReference{changed, b}, []MonthReference{a}) {
		t.Fatal("accepted a changed existing reference")
	}
	if err := validateMonthReferences(IndexStatePreview, []MonthReference{a, a}); err == nil {
		t.Fatal("accepted duplicate periods")
	}
	if err := validateMonthReferences(IndexStatePreview, []MonthReference{b, a}); err == nil {
		t.Fatal("accepted non-canonical month order")
	}
}

func installPublicationMonth(t *testing.T, root string, month int, release string, cell era5hourly.Cell, score uint8) Catalog {
	t.Helper()
	plan, mask := testPlanAndMask(t, month, []era5hourly.Cell{cell}, []era5hourly.Cell{cell})
	builder, err := New(Config{StagingRoot: root, ReleaseID: release, MaximumZoom: 1}, plan, mask)
	if err != nil {
		t.Fatal(err)
	}
	result := testResult(0, cell, score)
	days, ok := plan.ExpectedDayCount(0)
	if !ok {
		t.Fatal("test plan lacks its expected day count")
	}
	result.Days = days
	if err := builder.Add(result); err != nil {
		t.Fatal(err)
	}
	if err := builder.Commit(); err != nil {
		t.Fatal(err)
	}
	catalog, err := ValidateRelease(filepath.Join(root, "releases", release), plan, mask)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func readTestFile(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func sameIndexForTest(left, right Index) bool {
	return left.Version == right.Version && left.IndexID == right.IndexID && left.State == right.State &&
		left.ReferencePeriod == right.ReferencePeriod && left.Geometry == right.Geometry && left.Source == right.Source &&
		left.PositiveDateline == right.PositiveDateline && sameProbability(left.Probability, right.Probability) &&
		left.ScalarSemantics == right.ScalarSemantics && left.MaximumZoom == right.MaximumZoom &&
		left.PointRecordBytes == right.PointRecordBytes && slicesEqualMonths(left.Months, right.Months)
}

func slicesEqualMonths(left, right []MonthReference) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestPublicationContextCancellationLeavesPointerUnchanged(t *testing.T) {
	climateRoot := t.TempDir()
	cell := era5hourly.Cell{Latitude: 0, Longitude: 0}
	catalog := installPublicationMonth(t, climateRoot, 1, "era5-month-01", cell, 61)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := PublishValidatedMonth(ctx, climateRoot, catalog)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(climateRoot, "current.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled publication exposed a pointer: %v", err)
	}
}
