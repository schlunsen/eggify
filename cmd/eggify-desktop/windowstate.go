package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// windowState is the main window's last frame, restored on the next launch
// (Tauri: tauri-plugin-window-state).
type windowState struct {
	X         int  `json:"x"`
	Y         int  `json:"y"`
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximised bool `json:"maximised"`
}

func windowStatePath(claudeDir string) string {
	return filepath.Join(claudeDir, "eggify", "desktop-window-state.json")
}

func loadWindowState(path string) (windowState, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return windowState{}, false
	}
	var s windowState
	if json.Unmarshal(data, &s) != nil || s.Width < minWidth || s.Height < minHeight {
		return windowState{}, false
	}
	return s, true
}

func saveWindowState(path string, s windowState) error {
	if s.Width < minWidth || s.Height < minHeight {
		return nil
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
