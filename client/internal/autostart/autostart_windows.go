//go:build windows

// Package autostart toggles launch-at-login for the desktop client. On Windows it
// uses the per-user Run registry key, pointing at the current executable.
package autostart

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

// appValue is the Run-key value name for this app.
const appValue = "ClipBridge"

// runKey is the per-user autostart registry path.
const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// Enabled reports whether the Run-key value for this app is present.
func Enabled() (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false, nil
	}
	defer k.Close()
	if _, _, err := k.GetStringValue(appValue); err != nil {
		return false, nil
	}
	return true, nil
}

// Set adds or removes the Run-key value pointing at the current executable.
func Set(enable bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !enable {
		_ = k.DeleteValue(appValue) // absent is fine
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue(appValue, exe)
}
