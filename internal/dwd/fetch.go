package dwd

import (
	"fmt"
	"net/url"
	"strings"
)

const OpenDataBaseURL = "https://opendata.dwd.de/weather/nwp/icon-eu/grib"

var RequiredFolders = []string{"t_2m", "relhum_2m", "tot_prec", "clct", "u_10m", "v_10m"}

var gribNames = map[string]string{
	"t_2m":      "T_2M",
	"relhum_2m": "RELHUM_2M",
	"tot_prec":  "TOT_PREC",
	"clct":      "CLCT",
	"u_10m":     "U_10M",
	"v_10m":     "V_10M",
}

func FileURL(runHour, folder, runID string, step int) (string, error) {
	if runHour == "" || runID == "" {
		return "", fmt.Errorf("run hour and run id are required")
	}
	gribName, ok := gribNames[folder]
	if !ok {
		return "", fmt.Errorf("unsupported DWD ICON-EU folder %q", folder)
	}
	file := fmt.Sprintf("icon-eu_europe_regular-lat-lon_single-level_%s_%03d_%s.grib2.bz2", runID, step, gribName)
	parts := []string{strings.TrimRight(OpenDataBaseURL, "/"), url.PathEscape(runHour), url.PathEscape(folder), url.PathEscape(file)}
	return strings.Join(parts, "/"), nil
}
