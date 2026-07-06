//go:build !windows && !darwin

// Package autostart toggles launch-at-login for the desktop client. On platforms
// without a supported mechanism it is a no-op.
package autostart

// Enabled always reports false on unsupported platforms.
func Enabled() (bool, error) { return false, nil }

// Set is a no-op on unsupported platforms.
func Set(enable bool) error { return nil }
