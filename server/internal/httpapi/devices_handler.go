package httpapi

import (
	"net/http"
	"strings"

	"github.com/mokeyjay/clipbridge/server/internal/store"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// devicePatchRequest patches a device's name and/or lifecycle status.
type devicePatchRequest struct {
	Name   *string `json:"name"`
	Status *string `json:"status"`
}

// applyDevicePatch renames and/or changes a device's status, applying the right
// side effects (closing live connections on disable, full revocation on revoke).
// allowRevoke gates the 'revoked' status to admin callers; users revoke via the
// dedicated DELETE endpoint instead.
func (s *Server) applyDevicePatch(w http.ResponseWriter, r *http.Request, device *store.Device, allowRevoke bool) {
	var req devicePatchRequest
	if !s.decodeJSON(w, r, &req) {
		return
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len(name) > 64 {
			s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "设备名称不合法")
			return
		}
		if err := s.store.UpdateDeviceName(device.ID, name); err != nil {
			s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "更新设备名称失败")
			return
		}
		s.audit(r, "device.rename", "device", device.ID, name, "")
	}
	if req.Status != nil {
		switch protocol.DeviceStatus(*req.Status) {
		case protocol.DeviceActive:
			if err := s.store.SetDeviceStatus(device.ID, protocol.DeviceActive); err != nil {
				s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "更新设备状态失败")
				return
			}
			s.audit(r, "device.enable", "device", device.ID, device.Name, "")
		case protocol.DeviceDisabled:
			if err := s.store.SetDeviceStatus(device.ID, protocol.DeviceDisabled); err != nil {
				s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "更新设备状态失败")
				return
			}
			// Disabled devices are rejected at auth time; close any live connection now.
			s.hub.CloseDevice(device.ID)
			s.audit(r, "device.disable", "device", device.ID, device.Name, "")
		case protocol.DeviceRevoked:
			if !allowRevoke {
				s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "请使用吊销操作")
				return
			}
			s.revokeDevice(device.ID)
			s.audit(r, "device.revoke", "device", device.ID, device.Name, "")
		default:
			s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "设备状态不合法")
			return
		}
	}
	updated, err := s.store.GetDeviceByID(device.ID)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "读取设备失败")
		return
	}
	writeJSON(w, http.StatusOK, toDeviceView(updated))
}

// revokeDevice performs full revocation: status -> revoked, all tokens revoked,
// a device.revoked event sent, and the live connection closed. Idempotent.
func (s *Server) revokeDevice(deviceID string) {
	_ = s.store.SetDeviceStatus(deviceID, protocol.DeviceRevoked)
	_ = s.store.RevokeDeviceTokens(deviceID)
	// Tell the device it has been revoked, then drop the connection.
	s.hub.NotifyDevice(deviceID, protocol.Event{Event: protocol.EventDeviceRevoked, OccurredAt: rfc3339(s.store.Now().Unix())})
	s.hub.CloseDevice(deviceID)
}
