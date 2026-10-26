// Written by AI (GPT-6.1 Sol), 2026-10-19.
// Package scorebands shares the band's one editable threshold with the site and generators.
package scorebands

import (
	_ "embed"
	"encoding/json"
)

//go:embed awful-maximum.json
var awfulMaximumJSON []byte

var AwfulMaximum = func() int {
	var minimum int
	if err := json.Unmarshal(awfulMaximumJSON, &minimum); err != nil {
		panic(err)
	}
	return minimum
}()

var Thresholds = [...]float64{0, float64(AwfulMaximum), 40, 55, 70, 85, 100}
