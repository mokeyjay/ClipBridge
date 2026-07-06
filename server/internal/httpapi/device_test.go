package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// TestDeviceSettingsAndEffectiveConfig covers the device profile, settings
// round-trip and three-layer effective config endpoints.
func TestDeviceSettingsAndEffectiveConfig(t *testing.T) {
	env := newTestEnv(t)
	u, _ := env.st.CreateUser("alice", "h")
	devID, tok := env.mintDevice(t, u.ID, "mac")

	// Profile.
	resp := env.deviceGET(t, tok, apiPrefix+"/device/profile")
	var prof deviceProfileView
	decodeBody(t, resp, &prof)
	if prof.DeviceID != devID || prof.Platform != "darwin" {
		t.Fatalf("profile = %+v", prof)
	}

	// Effective config defaults.
	resp = env.deviceGET(t, tok, apiPrefix+"/device/effective-config")
	var eff protocol.EffectiveConfig
	decodeBody(t, resp, &eff)
	if eff.MaxSyncSizeBytes != 104857600 || len(eff.AllowedTypes) != 4 {
		t.Fatalf("effective = %+v", eff)
	}

	// Override device allowed types to text-only and verify it round-trips.
	patch := deviceSettingsDTO{
		MaxSyncSizeInherit:     true,
		AllowedTypesInherit:    false,
		AllowedTypes:           []protocol.ContentType{protocol.ContentText},
		MaxAutoUploadInherit:   true,
		MaxAutoDownloadInherit: true,
	}
	resp = env.devicePATCH(t, tok, apiPrefix+"/device/settings", patch)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch settings status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.deviceGET(t, tok, apiPrefix+"/device/effective-config")
	decodeBody(t, resp, &eff)
	if len(eff.AllowedTypes) != 1 || eff.AllowedTypes[0] != protocol.ContentText {
		t.Errorf("effective allowed types = %v, want [text]", eff.AllowedTypes)
	}
}

// TestUploadRejectsDisallowedType verifies the server defends against an upload
// whose content type the source device's effective config forbids.
func TestUploadRejectsDisallowedType(t *testing.T) {
	env := newTestEnv(t)
	u, _ := env.st.CreateUser("alice", "h")
	srcID, srcTok := env.mintDevice(t, u.ID, "src")
	dstID, dstTok := env.mintDevice(t, u.ID, "dst")

	srcConn, _ := env.dialDevice(t, srcTok)
	defer srcConn.Close()
	dstConn, _ := env.dialDevice(t, dstTok)
	defer dstConn.Close()
	if !waitFor(func() bool { return env.hub.IsDeviceOnline(srcID) && env.hub.IsDeviceOnline(dstID) }, 2*time.Second) {
		t.Fatal("devices not online")
	}

	// Restrict the source device to images only, then try to upload text.
	patch := deviceSettingsDTO{
		MaxSyncSizeInherit: true, MaxAutoUploadInherit: true, MaxAutoDownloadInherit: true,
		AllowedTypesInherit: false, AllowedTypes: []protocol.ContentType{protocol.ContentImage},
	}
	env.devicePATCH(t, srcTok, apiPrefix+"/device/settings", patch).Body.Close()

	payload := []byte("hello")
	sum := sha256.Sum256(payload)
	manifest := protocol.UploadManifest{
		ProtocolVersion: protocol.ProtocolVersion, ItemID: uuid.NewString(), ContentType: protocol.ContentText,
		CiphertextSizeBytes: int64(len(payload)), CiphertextSHA256: hex.EncodeToString(sum[:]),
		ChunkSizeBytes: 65536, TotalChunks: 1,
		Deliveries: []protocol.DeliveryTarget{{TargetDeviceID: dstID, WrappedDEK: "x"}},
	}
	ct, body := buildUpload(t, manifest, payload)
	resp := env.deviceUpload(t, srcTok, ct, body)
	var errResp protocol.ErrorResponse
	decodeBody(t, resp, &errResp)
	if errResp.Error.Code != protocol.ErrContentTypeNotAllowed {
		t.Errorf("code = %q, want CONTENT_TYPE_NOT_ALLOWED", errResp.Error.Code)
	}
}
