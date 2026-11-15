package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/schlunsen/eggify/internal/sessionmedia"
)

// show_media: the agent presents an audio or video file it made. Nothing is
// sent anywhere: the call itself (kept in the session's messages) is what the
// Eggify server reads to put a player in the chat, on the web and on iOS.
// The server applies its own checks (internal/sessionmedia) before serving it.

func mediaToolDefinitions() []Tool {
	return []Tool{{
		Name: sessionmedia.ShowMediaTool,
		Description: "Show an audio or video file you made (a render, a recording, generated music or speech) to the user as a player in the Eggify chat, on the computer and on their phone. " +
			"Use it once a file is ready; give the path and a short title. Formats: mp4, mov, m4v, webm, mkv, mp3, m4a, aac, wav, aiff, flac, ogg, opus. " +
			"The file must be inside your working directory or your scratchpad. MP4 (H.264/AAC) and M4A/MP3 play everywhere; other formats may need converting for the phone.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"path":  {Type: "string", Description: "The file (absolute, or relative to your working directory)"},
				"title": {Type: "string", Description: "A short title shown on the player (optional)"},
			},
			Required: []string{"path"},
		},
	}}
}

func (s *MCPServer) toolShowMedia(args map[string]interface{}) CallToolResult {
	p, _ := args["path"].(string)
	p = strings.TrimSpace(p)
	if p == "" {
		return mcpErrorResult("path is required")
	}
	if !sessionmedia.IsMediaPath(p) {
		return mcpErrorResult("not an audio or video file: use one of mp4, mov, m4v, webm, mkv, mp3, m4a, aac, wav, aiff, flac, ogg, opus")
	}
	abs := p
	if strings.HasPrefix(abs, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			abs = filepath.Join(home, abs[2:])
		}
	} else if !filepath.IsAbs(abs) {
		dir := s.workDir
		if dir == "" {
			dir, _ = os.Getwd()
		}
		abs = filepath.Join(dir, abs)
	}
	info, err := os.Stat(abs)
	if err != nil || !info.Mode().IsRegular() {
		return mcpErrorResult("file not found: " + abs)
	}
	if !sessionmedia.LooksLikeMedia(abs) {
		return mcpErrorResult("the file does not look like audio or video (is it finished writing?): " + abs)
	}
	note := ""
	switch strings.ToLower(filepath.Ext(abs)) {
	case ".webm", ".mkv", ".ogg", ".oga", ".opus":
		note = " The iPhone can't play this format directly; the user can convert it from the player, or render an MP4/M4A copy."
	}
	return CallToolResult{Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Shown in the Eggify chat as a player: %s (%s).%s", abs, humanSize(info.Size()), note)}}}
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
