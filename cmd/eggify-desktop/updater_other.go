//go:build !darwin

package main

import "errors"

// Self-update is macOS-only for now; checks still work (they only read the
// manifest), installs refuse.

var errUpdateUnsupported = errors.New("self-update is only supported on macOS")

type platformVerifier struct{}

func (platformVerifier) Verify(string) (bundleSignature, error) {
	return bundleSignature{}, errUpdateUnsupported
}

func extractZip(string, string) error           { return errUpdateUnsupported }
func clearQuarantine(string) error              { return nil }
func swapBundles(string, string) error          { return errUpdateUnsupported }
func scheduleRelaunch(string) error             { return errUpdateUnsupported }
func platformOSVersion() string                 { return "" }
func parseCodesignInfo(string) (string, string) { return "", "" }
