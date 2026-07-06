package httpapi

import (
	"errors"
	"net/http"

	"github.com/mokeyjay/clipbridge/server/internal/store"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// handleGetUserSettings returns the caller's sync-policy template.
func (s *Server) handleGetUserSettings(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	us, err := s.store.GetUserSettings(p.SubjectID)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "读取设置失败")
		return
	}
	writeJSON(w, http.StatusOK, userSettingsView{
		MaxSyncSizeBytes: us.MaxSyncSizeBytes, AllowedTypes: us.AllowedTypes,
		MaxAutoUploadSizeBytes: us.MaxAutoUploadSizeBytes, MaxAutoDownloadSizeBytes: us.MaxAutoDownloadSizeBytes,
		FileTTLDays: us.FileTTLDays,
	})
}

// updateUserSettingsRequest patches a subset of the user-level template.
type updateUserSettingsRequest struct {
	MaxSyncSizeBytes         *int64                  `json:"max_sync_size_bytes"`
	AllowedTypes             *[]protocol.ContentType `json:"allowed_types"`
	MaxAutoUploadSizeBytes   *int64                  `json:"max_auto_upload_size_bytes"`
	MaxAutoDownloadSizeBytes *int64                  `json:"max_auto_download_size_bytes"`
	FileTTLDays              *int64                  `json:"file_ttl_days"`
}

// handleUpdateUserSettings patches the caller's template and notifies their
// online devices to re-fetch effective config.
func (s *Server) handleUpdateUserSettings(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var req updateUserSettingsRequest
	if !s.decodeJSON(w, r, &req) {
		return
	}
	us, err := s.store.GetUserSettings(p.SubjectID)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "读取设置失败")
		return
	}
	if req.MaxSyncSizeBytes != nil {
		if *req.MaxSyncSizeBytes <= 0 {
			s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "最大同步尺寸必须为正")
			return
		}
		us.MaxSyncSizeBytes = *req.MaxSyncSizeBytes
	}
	if req.AllowedTypes != nil {
		if !validContentTypes(*req.AllowedTypes) {
			s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "内容类型不合法")
			return
		}
		us.AllowedTypes = *req.AllowedTypes
	}
	if req.MaxAutoUploadSizeBytes != nil {
		if *req.MaxAutoUploadSizeBytes < 0 {
			s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "尺寸不能为负")
			return
		}
		us.MaxAutoUploadSizeBytes = *req.MaxAutoUploadSizeBytes
	}
	if req.MaxAutoDownloadSizeBytes != nil {
		if *req.MaxAutoDownloadSizeBytes < 0 {
			s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "尺寸不能为负")
			return
		}
		us.MaxAutoDownloadSizeBytes = *req.MaxAutoDownloadSizeBytes
	}
	if req.FileTTLDays != nil {
		if *req.FileTTLDays < 1 || *req.FileTTLDays > 365 {
			s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "文件有效期需为 1 到 365 天")
			return
		}
		us.FileTTLDays = *req.FileTTLDays
	}
	if err := s.store.UpdateUserSettings(us); err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "更新设置失败")
		return
	}
	// 操作日志只记「更新了同步策略」这一事实，具体值不入日志。
	s.audit(r, "user.update_settings", "settings", p.SubjectID, p.Username, "")
	s.hub.NotifyUserDevices(p.SubjectID, protocol.Event{Event: protocol.EventConfigChanged, OccurredAt: rfc3339(s.store.Now().Unix())})
	s.handleGetUserSettings(w, r)
}

// handleListUserDevices lists the caller's own devices.
func (s *Server) handleListUserDevices(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	s.writeDeviceList(w, r, p.SubjectID)
}

// handleUpdateUserDevice renames or enables/disables the caller's own device.
func (s *Server) handleUpdateUserDevice(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	device, ok := s.ownedDevice(w, r, p.SubjectID, r.PathValue("id"))
	if !ok {
		return
	}
	s.applyDevicePatch(w, r, device, false) // users may not set 'revoked' via PATCH
}

// handleRevokeUserDevice revokes the caller's own device, or deletes the record
// when it is already revoked (lets the user clear stale revoked entries).
func (s *Server) handleRevokeUserDevice(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	device, ok := s.ownedDevice(w, r, p.SubjectID, r.PathValue("id"))
	if !ok {
		return
	}
	if device.Status == protocol.DeviceRevoked {
		if err := s.store.DeleteDevice(device.ID); err != nil {
			s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "删除设备失败")
			return
		}
		s.audit(r, "device.delete", "device", device.ID, device.Name, "")
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	s.revokeDevice(device.ID)
	s.audit(r, "device.revoke", "device", device.ID, device.Name, "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ownedDevice loads a device and verifies it belongs to userID, else writes 404.
func (s *Server) ownedDevice(w http.ResponseWriter, r *http.Request, userID, deviceID string) (*store.Device, bool) {
	device, err := s.store.GetDeviceByID(deviceID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && device.UserID != userID) {
		s.writeError(w, r, http.StatusNotFound, protocol.ErrorCode("NOT_FOUND"), "设备不存在")
		return nil, false
	}
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "读取设备失败")
		return nil, false
	}
	return device, true
}

// writeDeviceList writes a user's devices as console views.
func (s *Server) writeDeviceList(w http.ResponseWriter, r *http.Request, userID string) {
	devices, err := s.store.ListDevicesByUser(userID)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "读取设备失败")
		return
	}
	views := make([]deviceView, 0, len(devices))
	for _, d := range devices {
		v := toDeviceView(d)
		v.Online = s.hub.IsDeviceOnline(d.ID)
		views = append(views, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": views})
}

// validContentTypes reports whether every entry is a known content type.
func validContentTypes(types []protocol.ContentType) bool {
	for _, t := range types {
		switch t {
		case protocol.ContentText, protocol.ContentImage, protocol.ContentFile, protocol.ContentRichText:
		default:
			return false
		}
	}
	return true
}

// contentTypeAllowed reports whether ct is present in the allowed list.
func contentTypeAllowed(allowed []protocol.ContentType, ct protocol.ContentType) bool {
	for _, a := range allowed {
		if a == ct {
			return true
		}
	}
	return false
}
