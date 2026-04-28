package grid

func InEurope(lon, lat float64) bool {
	return EuropeBounds.Contains(lon, lat)
}
