package pipeline

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// GRIBMetadata contains the keys whose meaning materially changes aggregation.
// It is deliberately smaller than either provider's GRIB vocabulary.
type GRIBMetadata struct {
	ShortName    string
	StepType     string
	StartStep    float64
	EndStep      float64
	Units        string
	PackingError float64
	// PackingQuantum is the exact source-value increment encoded by GRIB's
	// binary and decimal scale factors: 2^E * 10^-D.
	PackingQuantum     float64
	BinaryScaleFactor  int
	DecimalScaleFactor int
}

// ParseECCodesJSON parses the GRIB identity, interval, units, error, and scale
// metadata requested by Decoder from ecCodes.
// Selected provider files must contain exactly one GRIB message.
func ParseECCodesJSON(data []byte) (GRIBMetadata, error) {
	var root struct {
		Messages []map[string]json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(data, &root); err != nil || root.Messages == nil {
		// grib_ls emits a top-level array; wrapped messages are also accepted
		// to keep the command boundary easy to fake in tests.
		if arrayErr := json.Unmarshal(data, &root.Messages); arrayErr != nil {
			if err == nil {
				err = arrayErr
			}
			return GRIBMetadata{}, fmt.Errorf("decode ecCodes metadata: %w", err)
		}
	}
	if len(root.Messages) != 1 {
		return GRIBMetadata{}, fmt.Errorf("ecCodes metadata contains %d messages; want 1", len(root.Messages))
	}
	message := root.Messages[0]
	shortName, err := metadataString(message["shortName"])
	if err != nil {
		return GRIBMetadata{}, fmt.Errorf("shortName: %w", err)
	}
	stepType, err := metadataString(message["stepType"])
	if err != nil {
		return GRIBMetadata{}, fmt.Errorf("stepType: %w", err)
	}
	startStep, err := metadataStepHours(message["startStep"])
	if err != nil {
		return GRIBMetadata{}, fmt.Errorf("startStep: %w", err)
	}
	endStep, err := metadataStepHours(message["endStep"])
	if err != nil {
		return GRIBMetadata{}, fmt.Errorf("endStep: %w", err)
	}
	units, err := metadataString(message["units"])
	if err != nil {
		return GRIBMetadata{}, fmt.Errorf("units: %w", err)
	}
	packingError, err := metadataFloat(message["packingError"])
	if err != nil {
		return GRIBMetadata{}, fmt.Errorf("packingError: %w", err)
	}
	if packingError < 0 {
		return GRIBMetadata{}, fmt.Errorf("packingError must be non-negative")
	}
	binaryScale, err := metadataInteger(message["binaryScaleFactor"])
	if err != nil {
		return GRIBMetadata{}, fmt.Errorf("binaryScaleFactor: %w", err)
	}
	decimalScale, err := metadataInteger(message["decimalScaleFactor"])
	if err != nil {
		return GRIBMetadata{}, fmt.Errorf("decimalScaleFactor: %w", err)
	}
	if binaryScale < -1022 || binaryScale > 1023 || decimalScale < -308 || decimalScale > 308 {
		return GRIBMetadata{}, fmt.Errorf("GRIB packing scale is outside finite float64 range")
	}
	quantum := math.Ldexp(1, binaryScale) * math.Pow10(-decimalScale)
	if quantum <= 0 || math.IsNaN(quantum) || math.IsInf(quantum, 0) {
		return GRIBMetadata{}, fmt.Errorf("GRIB packing quantum is not finite and positive")
	}
	return GRIBMetadata{
		ShortName: shortName, StepType: strings.ToLower(stepType), StartStep: startStep, EndStep: endStep,
		Units: units, PackingError: packingError, PackingQuantum: quantum,
		BinaryScaleFactor: binaryScale, DecimalScaleFactor: decimalScale,
	}, nil
}

func metadataInteger(raw json.RawMessage) (int, error) {
	value, err := metadataFloat(raw)
	if err != nil {
		return 0, err
	}
	if math.Trunc(value) != value || value < math.MinInt32 || value > math.MaxInt32 {
		return 0, fmt.Errorf("invalid integer %g", value)
	}
	return int(value), nil
}

func metadataString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("missing value")
	}
	var wrapped struct {
		Value json.RawMessage `json:"value"`
	}
	value := raw
	if raw[0] == '{' {
		if err := json.Unmarshal(raw, &wrapped); err != nil {
			return "", err
		}
		value = wrapped.Value
	}
	var text string
	if err := json.Unmarshal(value, &text); err == nil {
		if strings.TrimSpace(text) == "" {
			return "", fmt.Errorf("empty value")
		}
		return text, nil
	}
	var number json.Number
	if err := json.Unmarshal(value, &number); err == nil {
		return number.String(), nil
	}
	return "", fmt.Errorf("unsupported value %s", value)
}

func metadataFloat(raw json.RawMessage) (float64, error) {
	text, err := metadataString(raw)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number %q", text)
	}
	return value, nil
}

// ecCodes normally emits forecast steps as bare hours, but uses an explicit
// unit when the GRIB step cannot be represented honestly in hours. Current
// ICON-EU zero-step messages, for example, are reported as "0m". Keep bare
// values backwards compatible and convert only the documented scalar duration
// suffixes; unknown or compound forms still fail closed.
func metadataStepHours(raw json.RawMessage) (float64, error) {
	text, err := metadataString(raw)
	if err != nil {
		return 0, err
	}
	multiplier := 1.0
	number := text
	if len(text) > 1 {
		switch text[len(text)-1] {
		case 's':
			multiplier, number = 1.0/3600.0, text[:len(text)-1]
		case 'm':
			multiplier, number = 1.0/60.0, text[:len(text)-1]
		case 'h':
			number = text[:len(text)-1]
		case 'd':
			multiplier, number = 24, text[:len(text)-1]
		}
	}
	value, err := strconv.ParseFloat(number, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("invalid forecast step %q", text)
	}
	return value * multiplier, nil
}

func InterpretMetadata(field Field, run time.Time, metadata GRIBMetadata) (Semantics, time.Time, time.Time, error) {
	if run.IsZero() || metadata.EndStep < metadata.StartStep || metadata.StartStep < 0 {
		return 0, time.Time{}, time.Time{}, fmt.Errorf("invalid GRIB step interval %.3f..%.3f", metadata.StartStep, metadata.EndStep)
	}
	start := run.UTC().Add(time.Duration(metadata.StartStep * float64(time.Hour)))
	end := run.UTC().Add(time.Duration(metadata.EndStep * float64(time.Hour)))
	var semantics Semantics
	switch metadata.StepType {
	case "instant":
		semantics = Instant
	case "avg", "average":
		semantics = IntervalMean
		// A solar flux averaged from model start is neither an interval mean nor
		// an accumulation yet. Normalization integrates the whole elapsed period
		// before differencing consecutive messages into interval energy.
		if isSolarField(field) && metadata.StartStep == 0 {
			semantics = CumulativeMean
		}
	case "max", "maximum":
		semantics = IntervalMaximum
	case "accum", "accumulation":
		if metadata.StartStep == 0 {
			semantics = CumulativeAccumulation
		} else {
			semantics = IntervalAccumulation
		}
	default:
		return 0, time.Time{}, time.Time{}, fmt.Errorf("unsupported GRIB stepType %q", metadata.StepType)
	}
	if (field == Rain || isSolarField(field)) && semantics != IntervalMean && semantics != IntervalAccumulation && semantics != CumulativeAccumulation && semantics != CumulativeMean {
		return 0, time.Time{}, time.Time{}, fmt.Errorf("%s must be an interval or accumulated field, got %q", field, metadata.StepType)
	}
	if field != Rain && !isSolarField(field) && (semantics == CumulativeAccumulation || semantics == CumulativeMean) {
		return 0, time.Time{}, time.Time{}, fmt.Errorf("%s cannot be a cumulative accumulation", field)
	}
	return semantics, start, end, nil
}
