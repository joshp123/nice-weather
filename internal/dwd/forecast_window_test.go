package dwd

import "testing"

func TestMapICONToWeatherInput(t *testing.T) {
	got := MapICONToWeatherInput(IconEUFields{
		T2MKelvin: 295.15,
		RelHum2M:  55,
		TotPrecMM: 1.5,
		CloudPct:  30,
		U10MMS:    3,
		V10MMS:    4,
	})
	if got.TempC != 22 || got.Humidity != 55 || got.RainMM != 1.5 || got.CloudPct != 30 || got.WindKMH != 18 {
		t.Fatalf("unexpected mapped input: %+v", got)
	}
}
