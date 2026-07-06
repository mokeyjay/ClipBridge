// Package credstore manages the device's on-disk configuration and credentials,
// modeled on an .ssh-style directory rather than the system keychain. Credentials
// (device token, HPKE private key, pinned server fingerprint) live under a 0700
// credentials/ subdirectory as 0600 files, separate from ordinary settings.
// See prd/03-security-and-e2ee.md §4.
package credstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// File/dir permission modes (Unix). Windows ACL tightening is handled separately
// by tightenWindowsACL.
const (
	dirMode  = 0o700
	fileMode = 0o600
)

// Credential file names within the credentials/ subdirectory.
const (
	deviceFile      = "device.json"
	tokenFile       = "device-token"
	privateKeyFile  = "hpke-private-key"
	fingerprintFile = "server-fingerprint"
	knownPeersFile  = "known-peers.json"
	profileFile     = "profile.json"
)

// Identity is the non-secret device identity stored in device.json.
type Identity struct {
	DeviceID     string `json:"device_id"`
	UserID       string `json:"user_id"`
	ServerID     string `json:"server_id"`
	ServerURL    string `json:"server_url"`     // device-port base URL, e.g. https://host:8443
	PublicKeyB64 string `json:"public_key_b64"` // this device's HPKE public key
	Version      int    `json:"version"`
}

// Store is a handle to a device configuration directory.
type Store struct {
	dir string // root config dir
}

// Open returns a Store rooted at dir, creating the directory tree with strict
// permissions if needed and repairing loose permissions on existing dirs.
func Open(dir string) (*Store, error) {
	s := &Store{dir: dir}
	for _, d := range []string{dir, s.credsDir()} {
		if err := os.MkdirAll(d, dirMode); err != nil {
			return nil, fmt.Errorf("credstore: 创建目录 %s: %w", d, err)
		}
	}
	if err := s.RepairPermissions(); err != nil {
		// Per decision: continue running but surface the issue to the caller.
		return s, err
	}
	return s, nil
}

func (s *Store) credsDir() string         { return filepath.Join(s.dir, "credentials") }
func (s *Store) credPath(n string) string { return filepath.Join(s.credsDir(), n) }

// Dir returns the root config directory (for sibling data like received files).
func (s *Store) Dir() string { return s.dir }

// IsPaired reports whether a device identity and token are present.
func (s *Store) IsPaired() bool {
	if _, err := os.Stat(s.credPath(deviceFile)); err != nil {
		return false
	}
	_, err := os.Stat(s.credPath(tokenFile))
	return err == nil
}

// SaveIdentity writes the device identity (non-secret) as 0600 JSON.
func (s *Store) SaveIdentity(id *Identity) error {
	if id.Version == 0 {
		id.Version = 1
	}
	data, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return fmt.Errorf("credstore: 编码设备身份: %w", err)
	}
	return s.writeSecret(deviceFile, data)
}

// LoadIdentity reads the device identity.
func (s *Store) LoadIdentity() (*Identity, error) {
	data, err := os.ReadFile(s.credPath(deviceFile))
	if err != nil {
		return nil, err
	}
	var id Identity
	if err := json.Unmarshal(data, &id); err != nil {
		return nil, fmt.Errorf("credstore: 解析设备身份: %w", err)
	}
	return &id, nil
}

// SaveToken / LoadToken persist the device bearer token.
func (s *Store) SaveToken(token string) error { return s.writeSecret(tokenFile, []byte(token)) }
func (s *Store) LoadToken() (string, error)   { return s.readSecretString(tokenFile) }

// SavePrivateKey / LoadPrivateKey persist the device HPKE private key bytes.
func (s *Store) SavePrivateKey(key []byte) error { return s.writeSecret(privateKeyFile, key) }
func (s *Store) LoadPrivateKey() ([]byte, error) {
	return os.ReadFile(s.credPath(privateKeyFile))
}

// SaveServerFingerprint / LoadServerFingerprint persist the pinned device-port
// certificate SHA-256 the client confirmed during pairing.
func (s *Store) SaveServerFingerprint(fp string) error {
	return s.writeSecret(fingerprintFile, []byte(fp))
}
func (s *Store) LoadServerFingerprint() (string, error) {
	return s.readSecretString(fingerprintFile)
}

// SaveKnownPeers 持久化对端设备公钥指纹的 TOFU 缓存（deviceID → 指纹）。
// 属于信任状态,与凭据同目录存放;解除配对(Reset)时随凭据一起清除。
func (s *Store) SaveKnownPeers(peers map[string]string) error {
	data, err := json.MarshalIndent(peers, "", "  ")
	if err != nil {
		return fmt.Errorf("credstore: 编码对端指纹缓存: %w", err)
	}
	return s.writeSecret(knownPeersFile, data)
}

// LoadKnownPeers 读取持久化的对端指纹 TOFU 缓存;文件不存在时返回空表。
func (s *Store) LoadKnownPeers() (map[string]string, error) {
	data, err := os.ReadFile(s.credPath(knownPeersFile))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	peers := map[string]string{}
	if err := json.Unmarshal(data, &peers); err != nil {
		return nil, fmt.Errorf("credstore: 解析对端指纹缓存: %w", err)
	}
	return peers, nil
}

// Profile holds ordinary local-only settings (no secrets), stored in profile.json.
type Profile struct {
	Theme         string `json:"theme"`          // light | dark | system
	Language      string `json:"language"`       // zh | en
	SyncDirection string `json:"sync_direction"` // bidirectional | upload_only | download_only
	NotifyPolicy  string `json:"notify_policy"`  // quiet | default | verbose
	Paused        bool   `json:"paused"`
	// Autostart records the desired launch-at-login state (mirrors the OS-level
	// setting written by the autostart package).
	Autostart bool `json:"autostart,omitempty"`
	// WindowsBackdrop 是 Windows 11 的窗口材质偏好："mica"(默认) | "acrylic"。
	// 仅 Windows 生效;Windows 10 无论何值都回退普通窗口。
	WindowsBackdrop string `json:"windows_backdrop,omitempty"`
	// TempDir overrides the received-files directory (empty = default under the
	// config dir). Local-only; never shared across devices.
	TempDir string `json:"temp_dir,omitempty"`
	// FileTTLOverrideDays is the device-local received-file retention override in
	// days. 0 means inherit the account-level default from the server.
	FileTTLOverrideDays int64 `json:"file_ttl_override_days,omitempty"`
	// Window* remember the settings window's last position/size so it reopens where
	// the user left it. Width/Height 0 means "unset" (center on first launch).
	WindowX      int `json:"window_x,omitempty"`
	WindowY      int `json:"window_y,omitempty"`
	WindowWidth  int `json:"window_width,omitempty"`
	WindowHeight int `json:"window_height,omitempty"`
}

// SaveProfile / LoadProfile persist non-secret local settings (0600 for simplicity).
func (s *Store) SaveProfile(p *Profile) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("credstore: 编码本地配置: %w", err)
	}
	return s.writeFileMode(filepath.Join(s.dir, profileFile), data)
}

// LoadProfile reads local settings, returning defaults when absent.
func (s *Store) LoadProfile() (*Profile, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, profileFile))
	if errors.Is(err, os.ErrNotExist) {
		return &Profile{Theme: "system", SyncDirection: "bidirectional", NotifyPolicy: "default"}, nil
	}
	if err != nil {
		return nil, err
	}
	var p Profile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("credstore: 解析本地配置: %w", err)
	}
	return &p, nil
}

// Reset removes all credentials (the destructive client reset).
func (s *Store) Reset() error {
	return os.RemoveAll(s.credsDir())
}

// writeSecret atomically writes a 0600 file under credentials/.
func (s *Store) writeSecret(name string, data []byte) error {
	return s.writeFileMode(s.credPath(name), data)
}

// writeFileMode writes data to path via a temp file + rename, enforcing fileMode.
func (s *Store) writeFileMode(path string, data []byte) error {
	// Ensure the parent dir exists: Reset() removes credentials/, so a re-pair
	// after a reset would otherwise fail writing the first secret.
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return fmt.Errorf("credstore: 创建目录 %s: %w", filepath.Dir(path), err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, fileMode); err != nil {
		return fmt.Errorf("credstore: 写入 %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("credstore: 重命名 %s: %w", path, err)
	}
	// Rename preserves the temp file's mode; ensure final mode regardless of umask.
	return os.Chmod(path, fileMode)
}

// readSecretString reads a credentials/ file as a trimmed string.
func (s *Store) readSecretString(name string) (string, error) {
	data, err := os.ReadFile(s.credPath(name))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// PermissionWarning describes a credential path whose permissions could not be
// fully tightened. Callers display a persistent security warning but keep running.
type PermissionWarning struct {
	Path string
	Mode os.FileMode
}

// RepairPermissions tightens directory and file permissions to the strict modes,
// returning a joined error listing any paths that could not be repaired. On
// Windows it tightens the config/credentials directory ACLs to the current user.
func (s *Store) RepairPermissions() error {
	if runtime.GOOS == "windows" {
		return s.tightenWindowsACL()
	}
	var warns []error
	chmod := func(p string, mode os.FileMode) {
		info, err := os.Stat(p)
		if err != nil {
			return // missing files are not yet an error
		}
		if info.Mode().Perm() != mode.Perm() {
			if err := os.Chmod(p, mode); err != nil {
				warns = append(warns, fmt.Errorf("%s -> %v: %w", p, mode, err))
			}
		}
	}
	chmod(s.dir, dirMode)
	chmod(s.credsDir(), dirMode)
	for _, n := range []string{deviceFile, tokenFile, privateKeyFile, fingerprintFile, knownPeersFile} {
		chmod(s.credPath(n), fileMode)
	}
	return errors.Join(warns...)
}
