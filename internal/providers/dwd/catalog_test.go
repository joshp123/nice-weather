package dwd

import "testing"

func TestCatalogueSeparatesRequiredAndPreferredFields(t *testing.T) {
	required := map[string]bool{}
	for _, field := range RequiredFields() {
		required[field.Name] = true
	}
	for _, name := range []string{
		"temperature_2m",
		"dewpoint_2m",
		"total_cloud_cover",
		"total_precipitation",
		"wind_u_10m",
		"wind_v_10m",
	} {
		if !required[name] {
			t.Errorf("required field %q is missing", name)
		}
	}
	for _, name := range []string{"wind_gust_10m", "direct_shortwave_down", "diffuse_shortwave_down"} {
		if required[name] {
			t.Errorf("preferred field %q unexpectedly blocks run completeness", name)
		}
		if _, err := FieldByName(name); err != nil {
			t.Errorf("preferred field %q is missing: %v", name, err)
		}
	}

}
