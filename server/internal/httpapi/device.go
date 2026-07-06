package httpapi

import (
	"net/http"

	"github.com/mokeyjay/clipbridge/server/internal/store"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// deviceProfileView is the calling device's own basic identity.
type deviceProfileView struct {
	DeviceID       string `json:"device_id"`
	UserID         string `json:"user_id"`
	Name           string `json:"name"`
	Platform       string `json:"platform"`
	ClientVersion  string `json:"client_version"`
	Status         string `json:"status"`
	KeyFingerprint string `json:"key_fingerprint"`
}

// handleDeviceProfile returns the calling device's own profile.
func (s *Server) handleDeviceProfile(w http.ResponseWriter, r *http.Request) {
	d := deviceFrom(r.Context()).device
	writeJSON(w, http.StatusOK, deviceProfileView{
		DeviceID: d.ID, UserID: d.UserID, Name: d.Name, Platform: string(d.Platform),
		ClientVersion: d.ClientVersion, Status: string(d.Status), KeyFingerprint: protocol.KeyFingerprint(d.HPKEPublicKey),
	})
}

// handleDevicePeers 返回调用方用户名下全部设备（含调用方自身，Self 标记），
// 供客户端「关于」页展示公钥指纹做跨设备人工互验（prd/03 §5.3）。
func (s *Server) handleDevicePeers(w http.ResponseWriter, r *http.Request) {
	dev := deviceFrom(r.Context()).device
	devices, err := s.store.ListDevicesByUser(dev.UserID)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "读取设备失败")
		return
	}
	peers := make([]protocol.PeerDevice, 0, len(devices))
	for _, d := range devices {
		peers = append(peers, protocol.PeerDevice{
			DeviceID: d.ID, Name: d.Name, Platform: string(d.Platform), Status: string(d.Status),
			Online: s.hub.IsDeviceOnline(d.ID), KeyFingerprint: protocol.KeyFingerprint(d.HPKEPublicKey),
			Self: d.ID == dev.ID,
		})
	}
	writeJSON(w, http.StatusOK, protocol.PeersResponse{Peers: peers})
}

// deviceSettingsDTO is the per-field inherit/override state for a device.
type deviceSettingsDTO struct {
	MaxSyncSizeInherit       bool                   `json:"max_sync_size_inherit"`
	MaxSyncSizeBytes         *int64                 `json:"max_sync_size_bytes"`
	AllowedTypesInherit      bool                   `json:"allowed_types_inherit"`
	AllowedTypes             []protocol.ContentType `json:"allowed_types"`
	MaxAutoUploadInherit     bool                   `json:"max_auto_upload_inherit"`
	MaxAutoUploadSizeBytes   *int64                 `json:"max_auto_upload_size_bytes"`
	MaxAutoDownloadInherit   bool                   `json:"max_auto_download_inherit"`
	MaxAutoDownloadSizeBytes *int64                 `json:"max_auto_download_size_bytes"`
}

// toDeviceSettingsDTO projects a store row to its wire form.
func toDeviceSettingsDTO(ds *store.DeviceSettings) deviceSettingsDTO {
	return deviceSettingsDTO{
		MaxSyncSizeInherit: ds.MaxSyncSizeInherit, MaxSyncSizeBytes: ds.MaxSyncSizeBytes,
		AllowedTypesInherit: ds.AllowedTypesInherit, AllowedTypes: ds.AllowedTypes,
		MaxAutoUploadInherit: ds.MaxAutoUploadInherit, MaxAutoUploadSizeBytes: ds.MaxAutoUploadSizeBytes,
		MaxAutoDownloadInherit: ds.MaxAutoDownloadInherit, MaxAutoDownloadSizeBytes: ds.MaxAutoDownloadSizeBytes,
	}
}

// handleGetDeviceSettings returns the calling device's inherit/override state.
func (s *Server) handleGetDeviceSettings(w http.ResponseWriter, r *http.Request) {
	d := deviceFrom(r.Context()).device
	ds, err := s.store.GetDeviceSettings(d.ID)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "读取设备设置失败")
		return
	}
	writeJSON(w, http.StatusOK, toDeviceSettingsDTO(ds))
}

// handleUpdateDeviceSettings replaces the calling device's inherit/override state.
func (s *Server) handleUpdateDeviceSettings(w http.ResponseWriter, r *http.Request) {
	d := deviceFrom(r.Context()).device
	var req deviceSettingsDTO
	if !s.decodeJSON(w, r, &req) {
		return
	}
	if !req.AllowedTypesInherit && !validContentTypes(req.AllowedTypes) {
		s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "内容类型不合法")
		return
	}
	// Reject negative/zero overrides where a positive value is required.
	for _, v := range []*int64{req.MaxSyncSizeBytes, req.MaxAutoUploadSizeBytes, req.MaxAutoDownloadSizeBytes} {
		if v != nil && *v < 0 {
			s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "尺寸不能为负")
			return
		}
	}
	ds := &store.DeviceSettings{
		DeviceID:                 d.ID,
		MaxSyncSizeInherit:       req.MaxSyncSizeInherit,
		MaxSyncSizeBytes:         req.MaxSyncSizeBytes,
		AllowedTypesInherit:      req.AllowedTypesInherit,
		AllowedTypes:             req.AllowedTypes,
		MaxAutoUploadInherit:     req.MaxAutoUploadInherit,
		MaxAutoUploadSizeBytes:   req.MaxAutoUploadSizeBytes,
		MaxAutoDownloadInherit:   req.MaxAutoDownloadInherit,
		MaxAutoDownloadSizeBytes: req.MaxAutoDownloadSizeBytes,
	}
	if err := s.store.UpdateDeviceSettings(ds); err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "更新设备设置失败")
		return
	}
	s.handleGetDeviceSettings(w, r)
}

// handleEffectiveConfig returns the three-layer resolved policy this device must
// enforce locally (also enforced defensively by the server on upload).
func (s *Server) handleEffectiveConfig(w http.ResponseWriter, r *http.Request) {
	d := deviceFrom(r.Context()).device
	cfg, err := s.store.EffectiveConfig(d.ID)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "求值有效配置失败")
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}
