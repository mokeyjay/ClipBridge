package store

import (
	"testing"

	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// seedDeviceForEval creates a user + device and returns ids for eval tests.
func seedDeviceForEval(t *testing.T, st *Store) (userID, deviceID string) {
	t.Helper()
	u, _ := st.CreateUser("alice", "h")
	code, _ := st.CreatePairingCode(u.ID, "ch", 300)
	req, _ := st.CreatePairingRequest(code, "Mac", protocol.PlatformDarwin, "0.1.0", "pk", "ph")
	d, err := st.ConfirmPairingRequest(u.ID, req.ID)
	if err != nil {
		t.Fatalf("device: %v", err)
	}
	return u.ID, d.ID
}

// TestEffectiveConfigDefaultsInherit verifies a fully-inheriting device resolves
// to the user defaults, clamped by the server instance ceiling.
func TestEffectiveConfigDefaultsInherit(t *testing.T) {
	st, _ := newTestStore(t)
	_ = st.EnsureServerSettings("srv")
	userID, deviceID := seedDeviceForEval(t, st)

	cfg, err := st.EffectiveConfig(deviceID)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if cfg.MaxSyncSizeBytes != 104857600 {
		t.Errorf("max = %d, want 100 MiB", cfg.MaxSyncSizeBytes)
	}
	if len(cfg.AllowedTypes) != 4 {
		t.Errorf("allowed types = %v, want 4", cfg.AllowedTypes)
	}
	if cfg.MaxAutoUploadSizeBytes != 10485760 {
		t.Errorf("auto upload = %d, want 10 MiB", cfg.MaxAutoUploadSizeBytes)
	}
	if cfg.FileTTLDays != 7 {
		t.Errorf("file ttl = %d, want 7", cfg.FileTTLDays)
	}

	// A user-level change to the retention flows into the effective config.
	us, _ := st.GetUserSettings(userID)
	us.FileTTLDays = 3
	if err := st.UpdateUserSettings(us); err != nil {
		t.Fatalf("update: %v", err)
	}
	cfg2, _ := st.EffectiveConfig(deviceID)
	if cfg2.FileTTLDays != 3 {
		t.Errorf("file ttl after update = %d, want 3", cfg2.FileTTLDays)
	}
}

// TestEffectiveConfigServerClamp verifies the server ceiling clamps a larger
// user/device value.
func TestEffectiveConfigServerClamp(t *testing.T) {
	st, _ := newTestStore(t)
	_ = st.EnsureServerSettings("srv")
	_ = st.UpdateServerSettings("ClipBridge", false, 50*1024*1024, 30, protocol.AllContentTypes()) // 50 MiB ceiling
	userID, deviceID := seedDeviceForEval(t, st)

	// User wants 200 MiB (above the ceiling).
	us, _ := st.GetUserSettings(userID)
	us.MaxSyncSizeBytes = 200 * 1024 * 1024
	_ = st.UpdateUserSettings(us)

	cfg, _ := st.EffectiveConfig(deviceID)
	if cfg.MaxSyncSizeBytes != 50*1024*1024 {
		t.Errorf("clamped max = %d, want 50 MiB", cfg.MaxSyncSizeBytes)
	}
}

// TestEffectiveConfigDeviceOverride verifies per-field device overrides win over
// the user default (and are still clamped for max size).
func TestEffectiveConfigDeviceOverride(t *testing.T) {
	st, _ := newTestStore(t)
	_ = st.EnsureServerSettings("srv")
	_, deviceID := seedDeviceForEval(t, st)

	override := int64(20 * 1024 * 1024)
	ds, _ := st.GetDeviceSettings(deviceID)
	ds.MaxSyncSizeInherit = false
	ds.MaxSyncSizeBytes = &override
	ds.AllowedTypesInherit = false
	ds.AllowedTypes = []protocol.ContentType{protocol.ContentText}
	if err := st.UpdateDeviceSettings(ds); err != nil {
		t.Fatalf("update device settings: %v", err)
	}

	cfg, _ := st.EffectiveConfig(deviceID)
	if cfg.MaxSyncSizeBytes != override {
		t.Errorf("override max = %d, want %d", cfg.MaxSyncSizeBytes, override)
	}
	if len(cfg.AllowedTypes) != 1 || cfg.AllowedTypes[0] != protocol.ContentText {
		t.Errorf("override types = %v, want [text]", cfg.AllowedTypes)
	}
	// Auto-upload still inherits the user default.
	if cfg.MaxAutoUploadSizeBytes != 10485760 {
		t.Errorf("auto upload = %d, want inherited 10 MiB", cfg.MaxAutoUploadSizeBytes)
	}
}

// TestEffectiveConfigInstanceTypeCeiling verifies the instance-level allowlist
// filters out types even when the user/device enables them.
func TestEffectiveConfigInstanceTypeCeiling(t *testing.T) {
	st, _ := newTestStore(t)
	_ = st.EnsureServerSettings("srv")
	// Instance disables file sync (allows everything except file).
	_ = st.UpdateServerSettings("ClipBridge", false, 100*1024*1024, 30,
		[]protocol.ContentType{protocol.ContentText, protocol.ContentImage, protocol.ContentRichText})
	userID, deviceID := seedDeviceForEval(t, st)

	// User explicitly enables all four types (including file).
	us, _ := st.GetUserSettings(userID)
	us.AllowedTypes = protocol.AllContentTypes()
	_ = st.UpdateUserSettings(us)

	cfg, _ := st.EffectiveConfig(deviceID)
	for _, ct := range cfg.AllowedTypes {
		if ct == protocol.ContentFile {
			t.Fatalf("file should be filtered by instance allowlist, got %v", cfg.AllowedTypes)
		}
	}
	if len(cfg.AllowedTypes) != 3 {
		t.Errorf("allowed types = %v, want 3 (file removed)", cfg.AllowedTypes)
	}
}
