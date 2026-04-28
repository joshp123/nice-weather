package artifact

import (
	"path/filepath"
	"strconv"
)

func TilePath(root, basis, profile, horizon string, z, x, y int) string {
	return filepath.Join(root, "tiles", basis, profile, horizon, intString(z), intString(x), intString(y)+".png")
}

func TileURL(basis, profile, horizon string) string {
	return "/artifacts/tiles/" + basis + "/" + profile + "/" + horizon + "/{z}/{x}/{y}.png"
}

func intString(value int) string {
	return strconv.Itoa(value)
}
