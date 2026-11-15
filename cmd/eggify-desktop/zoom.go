package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
)

// Page zoom steps, like a browser's (⌘+ / ⌘- / ⌘0). The page reflows to the
// window at each step, so nothing scrolls sideways.
var zoomLevels = []float64{0.5, 0.67, 0.75, 0.8, 0.9, 1, 1.1, 1.25, 1.5, 1.75, 2, 2.5, 3}

const defaultZoom = 1.0

// nextZoom returns the step after cur in the given direction (+1 in, -1 out),
// staying within the range. A value between steps moves to the nearest step in
// that direction.
func nextZoom(cur float64, dir int) float64 {
	const eps = 1e-6
	if dir > 0 {
		for _, l := range zoomLevels {
			if l > cur+eps {
				return l
			}
		}
		return zoomLevels[len(zoomLevels)-1]
	}
	for i := len(zoomLevels) - 1; i >= 0; i-- {
		if zoomLevels[i] < cur-eps {
			return zoomLevels[i]
		}
	}
	return zoomLevels[0]
}

func zoomStatePath(claudeDir string) string {
	return filepath.Join(claudeDir, "eggify", "desktop-zoom.json")
}

// loadZoom reads the saved zoom; anything missing or out of range is 100%.
func loadZoom(path string) float64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return defaultZoom
	}
	var s struct {
		Zoom float64 `json:"zoom"`
	}
	if json.Unmarshal(data, &s) != nil || math.IsNaN(s.Zoom) || s.Zoom < zoomLevels[0] || s.Zoom > zoomLevels[len(zoomLevels)-1] {
		return defaultZoom
	}
	return s.Zoom
}

func saveZoom(path string, zoom float64) error {
	data, err := json.Marshal(struct {
		Zoom float64 `json:"zoom"`
	}{zoom})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
