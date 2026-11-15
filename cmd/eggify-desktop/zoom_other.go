//go:build !darwin

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// applyZoom zooms the window's web content; zoom 1 is 100%. Off macOS Wails'
// SetZoom is real page zoom.
func applyZoom(w *application.WebviewWindow, zoom float64) {
	if w != nil {
		w.SetZoom(zoom)
	}
}
