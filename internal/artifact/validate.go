package artifact

import "fmt"

func Validate(manifest Manifest) error {
	if manifest.Version != 1 {
		return fmt.Errorf("manifest version must be 1")
	}
	if len(manifest.Layers) == 0 {
		return fmt.Errorf("manifest must contain layers")
	}
	for _, layer := range manifest.Layers {
		if layer.Basis == "" || layer.Profile == "" || layer.Horizon == "" || layer.TileURL == "" {
			return fmt.Errorf("manifest layer is incomplete: %+v", layer)
		}
	}
	return nil
}
