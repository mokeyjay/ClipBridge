//go:build windows

package credstore

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"syscall"
)

// createNoWindow is the CREATE_NO_WINDOW process flag: run the console helper
// (icacls) without spawning a visible console window (avoids the startup cmd flash).
const createNoWindow = 0x08000000

// tightenWindowsACL best-effort ensures the current user has full control over the
// config tree. It is deliberately ADDITIVE — it never strips inherited ACEs:
//
//   - The config dir lives under %AppData%\Roaming, whose default ACLs already
//     exclude other standard users, so credentials aren't readable across accounts
//     without us removing anything.
//   - The earlier /inheritance:r approach removed the inherited ACEs that grant the
//     user access; when the explicit re-grant didn't take on a leaf file, the user
//     was locked out of device.json on the second launch ("Access is denied"). A
//     pure /grant can't lock anyone out because existing (inherited) access stays.
//   - It grants by the user's SID (not name), which resolves correctly for
//     Microsoft and domain accounts, and runs with CREATE_NO_WINDOW so there's no
//     startup console flash; one recursive call covers credentials/ and data below.
func (s *Store) tightenWindowsACL() error {
	principal := currentUserPrincipal()
	if principal == "" {
		return nil // can't determine the user; skip rather than risk a lockout
	}
	cmd := exec.Command("icacls", s.dir, "/grant", principal+":(OI)(CI)F", "/T", "/C", "/Q")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("icacls %s: %w (%s)", s.dir, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// currentUserPrincipal returns the icacls principal for the current user: the SID
// (prefixed with "*", most reliable) when available, else DOMAIN\User, else empty.
func currentUserPrincipal() string {
	if u, err := user.Current(); err == nil {
		if u.Uid != "" {
			return "*" + u.Uid
		}
		if u.Username != "" {
			return u.Username
		}
	}
	if name := os.Getenv("USERNAME"); name != "" {
		if dom := os.Getenv("USERDOMAIN"); dom != "" {
			return dom + "\\" + name
		}
		return name
	}
	return ""
}
