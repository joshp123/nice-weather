// Written by AI on 2026.10.19 by model GPT-6.1 Sol.
package outlook

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ForecastCandidates judges the same tomorrow records as GenerateWithStatic,
// without rendering posters. It is used to compare app and outlook rankings.
func ForecastCandidates(ctx context.Context, source, placesSource, staticSource string, now time.Time) ([]Place, string, error) {
	body, err := readSource(ctx, source, "v6/forecast/current.json")
	if err != nil {
		return nil, "", err
	}
	var pointer Pointer
	if err = json.Unmarshal(body, &pointer); err != nil {
		return nil, "", err
	}
	if pointer.Run == "" || strings.ContainsAny(pointer.Run, "/\\") {
		return nil, "", fmt.Errorf("invalid forecast run")
	}
	firstDate, err := time.Parse("2006-01-02", pointer.FirstDate)
	if err != nil {
		return nil, "", err
	}
	tomorrow := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day()+1, 0, 0, 0, 0, time.UTC)
	day := int(tomorrow.Sub(firstDate) / (24 * time.Hour))
	if day < 0 || day >= pointer.Days {
		return nil, "", fmt.Errorf("tomorrow outside forecast")
	}
	inputs, err := loadForecastPlaceInputs(ctx, source, staticSource, pointer, day, int(tomorrow.Month()))
	if err != nil {
		return nil, "", err
	}
	body, err = readSource(ctx, placesSource, "places.tsv")
	if err != nil {
		return nil, "", err
	}
	places, err := readPlaces(body, inputs.offsets)
	if err != nil {
		return nil, "", err
	}
	if err = inputs.judge(places); err != nil {
		return nil, "", err
	}
	return places, tomorrow.Format("2006-01-02"), nil
}
