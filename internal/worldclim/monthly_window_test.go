package worldclim

import "testing"

func TestMapWorldClimToWeatherInput(t *testing.T) {
	got := MapWorldClimToWeatherInput(MonthlyFields{
		TAvgC:       24,
		PrecMM:      31,
		SRadKJM2Day: 22000,
		WindMS:      4,
		VaprKPA:     1.8,
		Latitude:    44,
		Month:       7,
	})
	if got.TempC != 24 {
		t.Fatalf("temp = %.1f, want 24", got.TempC)
	}
	if got.RainMM != 1 {
		t.Fatalf("rain = %.1f, want 1", got.RainMM)
	}
	if got.WindKMH != 14.4 {
		t.Fatalf("wind = %.1f, want 14.4", got.WindKMH)
	}
	if got.Humidity <= 0 || got.Humidity > 100 {
		t.Fatalf("humidity out of range: %.1f", got.Humidity)
	}
	if got.CloudPct < 0 || got.CloudPct > 100 {
		t.Fatalf("cloud out of range: %.1f", got.CloudPct)
	}
}
