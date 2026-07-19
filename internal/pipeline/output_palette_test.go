package pipeline

import (
	"fmt"
	"image/color"
	"math"
	"testing"

	"nice-weather-finder/internal/delivery"
)

func TestCanonicalPaletteUsesOKLabInterpolation(t *testing.T) {
	palette, err := scorePalette(delivery.CanonicalPalette())
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]string{
		0: "#34364F", 10: "#3F415C", 20: "#4A4D69", 30: "#5C5F7C",
		40: "#7D7E94", 50: "#908D9A", 60: "#B3A49E", 70: "#D1B49A",
		80: "#DEB387", 90: "#EC9A65", 100: "#F27643",
	}
	for score, wantHex := range want {
		if got := paletteHex(palette[score]); got != wantHex {
			t.Errorf("score %d = %s; want %s", score, got, wantHex)
		}
	}
	for _, stop := range delivery.CanonicalPalette() {
		if got := paletteHex(palette[stop.Score]); got != stop.Color {
			t.Errorf("score %d endpoint = %s; want exact stop %s", stop.Score, got, stop.Color)
		}
	}
}

func TestOKLabConversionMatchesReferencePrimaries(t *testing.T) {
	tests := []struct {
		name string
		rgb  color.NRGBA
		want okLab
	}{
		{name: "red", rgb: color.NRGBA{R: 255, A: 255}, want: okLab{l: 0.627955, a: 0.224863, b: 0.125846}},
		{name: "green", rgb: color.NRGBA{G: 255, A: 255}, want: okLab{l: 0.866440, a: -0.233888, b: 0.179498}},
		{name: "blue", rgb: color.NRGBA{B: 255, A: 255}, want: okLab{l: 0.452014, a: -0.032457, b: -0.311528}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := colorToOKLab(test.rgb)
			if abs(got.l-test.want.l) > 0.000001 || abs(got.a-test.want.a) > 0.000001 || abs(got.b-test.want.b) > 0.000001 {
				t.Fatalf("OKLab = %+v; want %+v", got, test.want)
			}
			if roundTrip := okLabToColor(got); roundTrip != test.rgb {
				t.Fatalf("round trip = %v; want %v", roundTrip, test.rgb)
			}
		})
	}
}

func TestCanonicalPaletteKeepsOpacityAndPerceivedOrder(t *testing.T) {
	palette, err := scorePalette(delivery.CanonicalPalette())
	if err != nil {
		t.Fatal(err)
	}
	previousLightness := colorToOKLab(palette[0]).l
	for score := 0; score <= 100; score++ {
		if palette[score].A != 255 {
			t.Fatalf("score %d alpha = %d; want 255", score, palette[score].A)
		}
		lightness := colorToOKLab(palette[score]).l
		// The fixed sunlit-orange Excellent anchors intentionally trade some
		// lightness for warmth above 84. Keep that one transition gradual; below
		// it, lightness must remain ordered without quantisation reversals.
		if score <= 84 && lightness+0.003 < previousLightness {
			t.Fatalf("score %d perceived lightness reversed: %.6f after %.6f", score, lightness, previousLightness)
		}
		if previousLightness-lightness > 0.016 {
			t.Fatalf("score %d has an abrupt perceived-lightness reversal: %.6f after %.6f", score, lightness, previousLightness)
		}
		previousLightness = lightness
	}
	if colorToOKLab(palette[100]).l <= colorToOKLab(palette[54]).l {
		t.Fatal("Excellent collapsed below the Mixed band in perceived lightness")
	}
}

func TestOKLabConversionClampsOutOfGamutColours(t *testing.T) {
	tests := []struct {
		value okLab
		want  color.NRGBA
	}{
		{value: okLab{l: 0.5, a: 1, b: 1}, want: color.NRGBA{R: 255, A: 255}},
		{value: okLab{l: 0.5, a: -1, b: -1}, want: color.NRGBA{R: 194, B: 255, A: 255}},
		{value: okLab{l: 1.2, a: 0.5, b: -0.5}, want: color.NRGBA{R: 255, B: 255, A: 255}},
	}
	for _, test := range tests {
		got := okLabToColor(test.value)
		if got != test.want {
			t.Errorf("out-of-gamut %+v = %v; want clamped %v", test.value, got, test.want)
		}
	}
}

func TestCanonicalPaletteBoundariesSurviveGreyscale(t *testing.T) {
	palette, err := scorePalette(delivery.CanonicalPalette())
	if err != nil {
		t.Fatal(err)
	}
	for _, boundary := range []int{25, 40, 55, 70, 85} {
		left, right := palette[boundary-1], palette[boundary]
		if difference := okLabDistance(left, right); difference < 0.01 {
			t.Errorf("boundary %d OKLab distance = %.4f; want at least 0.01", boundary, difference)
		}
		if difference := abs(relativeLuminance(left) - relativeLuminance(right)); difference < 0.01 {
			t.Errorf("boundary %d greyscale luminance difference = %.4f; want at least 0.01", boundary, difference)
		}
	}
}

func okLabDistance(left, right color.NRGBA) float64 {
	a, b := colorToOKLab(left), colorToOKLab(right)
	dl, da, db := a.l-b.l, a.a-b.a, a.b-b.b
	return math.Sqrt(dl*dl + da*da + db*db)
}

func abs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

func relativeLuminance(value color.NRGBA) float64 {
	return 0.2126*srgbToLinear(float64(value.R)/255) +
		0.7152*srgbToLinear(float64(value.G)/255) +
		0.0722*srgbToLinear(float64(value.B)/255)
}

func paletteHex(value color.NRGBA) string {
	return fmt.Sprintf("#%02X%02X%02X", value.R, value.G, value.B)
}
