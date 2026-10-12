package dwd

import "fmt"

const DefaultBaseURL = "https://opendata.dwd.de/weather/nwp/icon-eu/grib"

// Field identifies one regular latitude/longitude ICON-EU single-level product.
// Required fields are the minimum set for publishing a usable forecast. Preferred
// fields improve the weather description but do not delay a run while DWD is
// still publishing them.
type Field struct {
	Name      string
	Directory string
	FileToken string
	Required  bool
}

var fields = []Field{
	{Name: "temperature_2m", Directory: "t_2m", FileToken: "T_2M", Required: true},
	{Name: "dewpoint_2m", Directory: "td_2m", FileToken: "TD_2M", Required: true},
	{Name: "total_cloud_cover", Directory: "clct", FileToken: "CLCT", Required: true},
	{Name: "total_precipitation", Directory: "tot_prec", FileToken: "TOT_PREC", Required: true},
	{Name: "wind_u_10m", Directory: "u_10m", FileToken: "U_10M", Required: true},
	{Name: "wind_v_10m", Directory: "v_10m", FileToken: "V_10M", Required: true},
	{Name: "wind_gust_10m", Directory: "vmax_10m", FileToken: "VMAX_10M"},
	{Name: "direct_shortwave_down", Directory: "aswdir_s", FileToken: "ASWDIR_S"},
	{Name: "diffuse_shortwave_down", Directory: "aswdifd_s", FileToken: "ASWDIFD_S"},
}

// Fields returns a copy so callers cannot mutate the provider catalogue.
func RequiredFields() []Field {
	result := make([]Field, 0, len(fields))
	for _, field := range fields {
		if field.Required {
			result = append(result, field)
		}
	}
	return result
}

func FieldByName(name string) (Field, error) {
	for _, field := range fields {
		if field.Name == name {
			return field, nil
		}
	}
	return Field{}, fmt.Errorf("unknown DWD ICON-EU field %q", name)
}
