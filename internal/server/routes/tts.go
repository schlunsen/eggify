package routes

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/server/handlers"
)

// RegisterTTS configures text-to-speech endpoints
func RegisterTTS(api fiber.Router, h *handlers.TTSHandler) {
	tts := api.Group("/tts")
	tts.Post("/generate", h.HandleTTS)
	tts.Get("/status", h.HandleTTSStatus)

	// Serve F5-TTS ONNX models as static files for browser WebGPU inference
	cwd, _ := os.Getwd()
	registerTTSModels(tts, filepath.Join(cwd, "models", "f5-tts"))
}

// ttsModelContentTypes are the only files served from the model folder, with
// the type they are served as. The folder is under the dashboard's origin, so
// an HTML, SVG or JS file dropped into it (agents can write to the working
// directory) must never render or run there.
var ttsModelContentTypes = map[string]string{
	".onnx":      "application/octet-stream",
	".data":      "application/octet-stream", // ONNX external weights
	".onnx_data": "application/octet-stream",
	".bin":       "application/octet-stream",
	".json":      "application/json",
	".txt":       "text/plain; charset=utf-8",
}

// registerTTSModels serves modelDir at /models/f5-tts (if it exists).
// Models are large (664MB transformer) so we set appropriate headers.
func registerTTSModels(tts fiber.Router, modelDir string) {
	if _, err := os.Stat(modelDir); err != nil {
		return
	}
	tts.Use("/models/f5-tts", ttsModelFileGuard)
	tts.Static("/models/f5-tts", modelDir, fiber.Static{
		Browse:   false,
		MaxAge:   86400 * 30, // Cache for 30 days
		Compress: false,      // ONNX files don't compress well
	})
}

// ttsModelFileGuard refuses files with other extensions and, after the static
// handler ran, forces a fixed Content-Type, nosniff and attachment so nothing
// from the folder is ever interpreted as a page or script.
func ttsModelFileGuard(c *fiber.Ctx) error {
	ext := strings.ToLower(path.Ext(c.Path()))
	contentType, ok := ttsModelContentTypes[ext]
	if !ok {
		return c.SendStatus(fiber.StatusNotFound)
	}
	if err := c.Next(); err != nil {
		return err
	}
	c.Set(fiber.HeaderXContentTypeOptions, "nosniff")
	c.Set(fiber.HeaderContentDisposition, "attachment")
	c.Set(fiber.HeaderContentSecurityPolicy, "default-src 'none'; sandbox")
	if c.Response().StatusCode() < 300 {
		c.Set(fiber.HeaderContentType, contentType)
	}
	return nil
}
