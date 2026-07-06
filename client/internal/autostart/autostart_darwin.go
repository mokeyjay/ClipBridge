//go:build darwin

// Package autostart toggles launch-at-login for the desktop client. On macOS it
// writes a per-user LaunchAgent plist pointing at the current executable.
package autostart

import (
	"fmt"
	"os"
	"path/filepath"
)

// label is the LaunchAgent identifier (and plist file base name).
const label = "com.clipbridge.desktop"

// plistTemplate launches the current executable at login.
const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
</dict>
</plist>
`

// plistPath returns the LaunchAgent file path under the user's home.
func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist"), nil
}

// Enabled reports whether the LaunchAgent plist exists.
func Enabled() (bool, error) {
	p, err := plistPath()
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(p); err != nil {
		return false, nil
	}
	return true, nil
}

// Set writes or removes the LaunchAgent plist for the current executable.
func Set(enable bool) error {
	p, err := plistPath()
	if err != nil {
		return err
	}
	if !enable {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(fmt.Sprintf(plistTemplate, label, exe)), 0o644)
}
