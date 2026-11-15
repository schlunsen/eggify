package server

import (
	"fmt"
	"os"
)

// setupTLS initializes TLS certificates if enabled
func (s *Server) setupTLS() error {
	// Remote access (ngrok) is an extra plain-HTTP listener fed by the
	// in-process tunnel (ngrok terminates TLS), so the local listener keeps
	// its own TLS setting even when a tunnel is configured.

	// Check if TLS is disabled via environment variable (useful for ngrok)
	if !s.config.TLS.Enabled || os.Getenv("EGGIFY_DISABLE_TLS") == "true" {
		return nil
	}

	certManager := NewCertificateManager(s.claudeDir)
	certManager.SetQuiet(s.quiet)
	tlsConfig, err := certManager.EnsureCertificates()
	if err != nil {
		return fmt.Errorf("failed to initialize TLS: %w", err)
	}
	s.tlsConfig = tlsConfig

	return nil
}
