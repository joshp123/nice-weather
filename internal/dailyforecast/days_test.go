package dailyforecast

import (
	"testing"
	"time"
)

func TestCivilDaytimeWindow(t *testing.T) {
	for _, test := range []struct{ name, zone, date, start, end string }{
		{"half-hour zone", "Asia/Kolkata", "2026-10-16", "2026-10-16T04:30:00Z", "2026-10-16T14:30:00Z"},
		{"east of date line", "Pacific/Kiritimati", "2026-10-16", "2026-10-15T20:00:00Z", "2026-10-16T06:00:00Z"},
		{"west of date line", "Pacific/Pago_Pago", "2026-10-16", "2026-10-16T21:00:00Z", "2026-10-17T07:00:00Z"},
		{"DST change", "Europe/Amsterdam", "2026-10-25", "2026-10-25T09:00:00Z", "2026-10-25T19:00:00Z"},
	} {
		t.Run(test.name, func(t *testing.T) {
			location, err := time.LoadLocation(test.zone)
			if err != nil {
				t.Fatal(err)
			}
			date, err := time.Parse("2006-01-02", test.date)
			if err != nil {
				t.Fatal(err)
			}
			start, end := daytimeWindow(date, location)
			if start.Format(time.RFC3339) != test.start || end.Format(time.RFC3339) != test.end {
				t.Fatalf("got %s..%s, want %s..%s", start, end, test.start, test.end)
			}
		})
	}
}
