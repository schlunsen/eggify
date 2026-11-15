//go:build !darwin

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// Without a menu bar to sit in, the theatre is a small window at the top
// centre of the primary screen.
func readNotchGeometry() notchGeometry { return notchGeometry{MenuHeight: 28} }

func placeNotchWindow(w *application.WebviewWindow, width, height int) {
	if w == nil {
		return
	}
	w.SetSize(width, height)
	if s, err := w.GetScreen(); err == nil && s != nil {
		w.SetPosition(s.WorkArea.X+(s.WorkArea.Width-width)/2, s.WorkArea.Y)
	}
}
