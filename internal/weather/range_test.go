package weather

import "testing"

func TestSummarizeRangeKeepsMeanAndVariationLegible(t *testing.T) {
	consistent := makeResults(70, 70, 70, 70, 70, 70, 70)
	volatile := makeResults(90, 90, 90, 90, 10, 10, 10)

	steady, err := SummarizeRange(consistent)
	if err != nil {
		t.Fatal(err)
	}
	mixed, err := SummarizeRange(volatile)
	if err != nil {
		t.Fatal(err)
	}
	if steady.Score != 70 || steady.NiceDays != 7 || steady.AwfulDays != 0 || steady.LongestPoorRun != 0 {
		t.Fatalf("consistent range = %+v", steady)
	}
	if mixed.Score != 56 || mixed.MedianScore != 90 || mixed.NiceDays != 4 || mixed.AwfulDays != 3 || mixed.LongestPoorRun != 3 {
		t.Fatalf("volatile range = %+v", mixed)
	}
	if steady.Score <= mixed.Score {
		t.Fatalf("seven consistently good days should outrank a three-day awful run: steady=%+v volatile=%+v", steady, mixed)
	}
}

func TestSummarizeRangeAveragesCanonicalComponents(t *testing.T) {
	days := []Result{
		{Available: true, Score: 80, Rating: Rating(80), Components: Components{Thermal: 90, Dryness: 100, Sun: 70, Wind: 60}},
		{Available: true, Score: 60, Rating: Rating(60), Components: Components{Thermal: 70, Dryness: 60, Sun: 50, Wind: 80}},
	}
	result, err := SummarizeRange(days)
	if err != nil {
		t.Fatal(err)
	}
	if result.Score != 70 || result.MedianScore != 70 || result.Rating != Rating(70) || result.Components != (Components{Thermal: 80, Dryness: 80, Sun: 60, Wind: 70}) {
		t.Fatalf("range = %+v", result)
	}
}

func TestSummarizeRangeRejectsMissingDays(t *testing.T) {
	if _, err := SummarizeRange(nil); err == nil {
		t.Fatal("empty range was accepted")
	}
	if _, err := SummarizeRange([]Result{{Available: true, Score: 70}, {}}); err == nil {
		t.Fatal("range with an unavailable day was accepted")
	}
}

func makeResults(scores ...int) []Result {
	results := make([]Result, len(scores))
	for index, score := range scores {
		results[index] = Result{Available: true, Score: score, Rating: Rating(score), Components: Components{Thermal: score, Dryness: score, Sun: score, Wind: score}}
	}
	return results
}
