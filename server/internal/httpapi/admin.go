package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/mokeyjay/clipbridge/server/internal/security"
	"github.com/mokeyjay/clipbridge/server/internal/store"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// handleGetServerSettings returns the instance configuration.
func (s *Server) handleGetServerSettings(w http.ResponseWriter, r *http.Request) {
	ss, err := s.store.GetServerSettings()
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "读取配置失败")
		return
	}
	writeJSON(w, http.StatusOK, serverSettingsView{
		ServerID: ss.ServerID, ServerName: ss.ServerName,
		MaxSyncSizeBytes: ss.MaxSyncSizeBytes, AllowedTypes: ss.AllowedTypes,
		CiphertextTTLSeconds: ss.CiphertextTTLSeconds,
		SyncLogRetentionDays: ss.SyncLogRetentionDays,
	})
}

// updateServerSettingsRequest is the admin settings patch body. Pointers let the
// admin update a subset of fields. (Self-registration was removed; admins add
// users explicitly.)
type updateServerSettingsRequest struct {
	ServerName           *string                 `json:"server_name"`
	MaxSyncSizeBytes     *int64                  `json:"max_sync_size_bytes"`
	AllowedTypes         *[]protocol.ContentType `json:"allowed_types"`
	SyncLogRetentionDays *int64                  `json:"sync_log_retention_days"`
}

// handleUpdateServerSettings patches the instance configuration and notifies all
// online devices to re-fetch effective config when the size ceiling or the
// allowed-type allowlist changes.
func (s *Server) handleUpdateServerSettings(w http.ResponseWriter, r *http.Request) {
	var req updateServerSettingsRequest
	if !s.decodeJSON(w, r, &req) {
		return
	}
	ss, err := s.store.GetServerSettings()
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "读取配置失败")
		return
	}
	policyChanged := false
	var changed []string // 操作日志用的变更字段名（不含值）
	if req.ServerName != nil {
		ss.ServerName = *req.ServerName
		changed = append(changed, "server_name")
	}
	if req.MaxSyncSizeBytes != nil {
		if *req.MaxSyncSizeBytes <= 0 {
			s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "最大同步尺寸必须为正")
			return
		}
		policyChanged = policyChanged || *req.MaxSyncSizeBytes != ss.MaxSyncSizeBytes
		ss.MaxSyncSizeBytes = *req.MaxSyncSizeBytes
		changed = append(changed, "max_sync_size_bytes")
	}
	if req.AllowedTypes != nil {
		if !validContentTypes(*req.AllowedTypes) {
			s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "包含未知的同步类型")
			return
		}
		policyChanged = policyChanged || !sameTypes(*req.AllowedTypes, ss.AllowedTypes)
		ss.AllowedTypes = *req.AllowedTypes
		changed = append(changed, "allowed_types")
	}
	if req.SyncLogRetentionDays != nil {
		if *req.SyncLogRetentionDays < 0 {
			s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "日志保留天数不能为负")
			return
		}
		ss.SyncLogRetentionDays = *req.SyncLogRetentionDays
		changed = append(changed, "sync_log_retention_days")
	}
	if err := s.store.UpdateServerSettings(ss.ServerName, false, ss.MaxSyncSizeBytes, ss.SyncLogRetentionDays, ss.AllowedTypes); err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "更新配置失败")
		return
	}
	if len(changed) > 0 {
		// 操作日志只记变更了哪些字段，不记具体值（prd/06 §5）。
		s.audit(r, "settings.update", "settings", "", ss.ServerName, strings.Join(changed, ","))
	}
	if policyChanged {
		// 实例上限/允许类型影响每台设备的有效配置，通知在线设备刷新。
		s.hub.NotifyAllDevices(protocol.Event{Event: protocol.EventConfigChanged, OccurredAt: rfc3339(s.store.Now().Unix())})
	}
	s.handleGetServerSettings(w, r)
}

// sameTypes reports whether two content-type lists contain the same set (used to
// decide whether to notify devices on an allowlist change; order-insensitive).
func sameTypes(a, b []protocol.ContentType) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[protocol.ContentType]bool, len(a))
	for _, t := range a {
		seen[t] = true
	}
	for _, t := range b {
		if !seen[t] {
			return false
		}
	}
	return true
}

// handleListUsers returns all users.
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers()
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "读取用户失败")
		return
	}
	views := make([]userView, 0, len(users))
	for _, u := range users {
		views = append(views, toUserView(u))
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": views})
}

// handleCreateUser creates a user with an admin-supplied password.
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req protocol.LoginRequest
	if !s.decodeJSON(w, r, &req) {
		return
	}
	if !validUsername(req.Username) || len(req.Password) < minPasswordLen {
		s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "用户名或密码不合法")
		return
	}
	hash, err := security.HashPassword(req.Password)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "处理密码失败")
		return
	}
	u, err := s.store.CreateUser(req.Username, hash)
	if err != nil {
		s.writeError(w, r, http.StatusConflict, protocol.ErrorCode("USERNAME_TAKEN"), "用户名已存在")
		return
	}
	s.audit(r, "user.create", "user", u.ID, u.Username, "")
	writeJSON(w, http.StatusCreated, toUserView(u))
}

// updateUserRequest patches a user's name and/or status.
type updateUserRequest struct {
	Username *string `json:"username"`
	Status   *string `json:"status"`
}

// handleUpdateUser renames or enables/disables a user. Disabling tears down the
// user's sessions and live connections immediately.
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	target, err := s.store.GetUserByID(id)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, r, http.StatusNotFound, protocol.ErrorCode("NOT_FOUND"), "用户不存在")
		return
	} else if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "读取用户失败")
		return
	}
	var req updateUserRequest
	if !s.decodeJSON(w, r, &req) {
		return
	}
	if req.Username != nil {
		if !validUsername(*req.Username) {
			s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "用户名不合法")
			return
		}
		if err := s.store.UpdateUsername(id, *req.Username); err != nil {
			s.writeError(w, r, http.StatusConflict, protocol.ErrorCode("USERNAME_TAKEN"), "用户名已存在")
			return
		}
		s.audit(r, "user.rename", "user", id, *req.Username, "")
	}
	if req.Status != nil {
		status := protocol.UserStatus(*req.Status)
		if status != protocol.UserActive && status != protocol.UserDisabled {
			s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "状态不合法")
			return
		}
		if err := s.store.SetUserStatus(id, status); err != nil {
			s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "更新状态失败")
			return
		}
		if status == protocol.UserDisabled {
			_ = s.store.DeleteSessionsForSubject(protocol.SubjectUser, id)
			s.hub.CloseUser(id)
			// Disabling a user revokes all of their devices (they must re-pair after
			// re-enable). Re-enabling does NOT touch device status — revoked stays revoked.
			s.revokeAllUserDevices(id)
			s.audit(r, "user.disable", "user", id, target.Username, "")
		} else {
			s.audit(r, "user.enable", "user", id, target.Username, "")
		}
	}
	updated, _ := s.store.GetUserByID(id)
	writeJSON(w, http.StatusOK, toUserView(updated))
}

// revokeAllUserDevices revokes every device owned by a user (best-effort).
func (s *Server) revokeAllUserDevices(userID string) {
	devices, err := s.store.ListDevicesByUser(userID)
	if err != nil {
		return
	}
	for _, d := range devices {
		if d.Status != protocol.DeviceRevoked {
			s.revokeDevice(d.ID)
		}
	}
}

// handleResetUserPassword sets a fresh random password and returns it once. The
// user's sessions are invalidated so the old password cannot keep a session.
func (s *Server) handleResetUserPassword(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	target, err := s.store.GetUserByID(id)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, r, http.StatusNotFound, protocol.ErrorCode("NOT_FOUND"), "用户不存在")
		return
	}
	password, err := security.RandomToken(18)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "生成密码失败")
		return
	}
	hash, err := security.HashPassword(password)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "处理密码失败")
		return
	}
	if err := s.store.UpdateUserPassword(id, hash); err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "更新密码失败")
		return
	}
	_ = s.store.DeleteSessionsForSubject(protocol.SubjectUser, id)
	// Resetting a password also revokes all the user's devices (equivalent to
	// disable+enable) so a leaked credential can't keep paired devices syncing.
	s.revokeAllUserDevices(id)
	// 只记录「重置了谁的密码」这一事实，新密码绝不入日志。
	s.audit(r, "user.reset_password", "user", id, target.Username, "")
	writeJSON(w, http.StatusOK, map[string]string{"password": password})
}

// handleAdminListUserDevices lists a given user's devices.
func (s *Server) handleAdminListUserDevices(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.writeDeviceList(w, r, id)
}

// handleAdminUpdateDevice enables, disables or revokes any device.
func (s *Server) handleAdminUpdateDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	device, err := s.store.GetDeviceByID(id)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, r, http.StatusNotFound, protocol.ErrorCode("NOT_FOUND"), "设备不存在")
		return
	}
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "读取设备失败")
		return
	}
	s.applyDevicePatch(w, r, device, true) // admins may set 'revoked'
}

// handleAdminDeleteDevice deletes a device record. Only revoked devices may be
// deleted, so an active/disabled device must be revoked first.
func (s *Server) handleAdminDeleteDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	device, err := s.store.GetDeviceByID(id)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, r, http.StatusNotFound, protocol.ErrorCode("NOT_FOUND"), "设备不存在")
		return
	}
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "读取设备失败")
		return
	}
	if device.Status != protocol.DeviceRevoked {
		s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "请先吊销设备再删除")
		return
	}
	if err := s.store.DeleteDevice(id); err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "删除设备失败")
		return
	}
	s.audit(r, "device.delete", "device", id, device.Name, "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleAdminStats returns instance-wide counts for the overview, including the
// number of devices currently online (from the live presence hub).
func (s *Server) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.CountUsers()
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "统计失败")
		return
	}
	devices, err := s.store.CountDevices()
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "统计失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{
		"user_count":          users,
		"device_count":        devices,
		"online_device_count": s.hub.OnlineDeviceCount(),
	})
}

// handleListSyncLogs returns sync-log entries (no plaintext) with resolved
// user/device names, filtered by ?result= and ?q=, paginated by ?page= (1-based,
// 20 per page). Responds with the page rows and the total matching count.
func (s *Server) handleListSyncLogs(w http.ResponseWriter, r *http.Request) {
	const pageSize = 20
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	result := r.URL.Query().Get("result")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	logs, total, err := s.store.ListSyncLogsFiltered(result, q, pageSize, (page-1)*pageSize)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "读取同步日志失败")
		return
	}
	views := make([]syncLogView, 0, len(logs))
	for _, l := range logs {
		views = append(views, toSyncLogView(l))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"logs": views, "total": total, "page": page, "page_size": pageSize,
	})
}

// handleUpdateAdminProfile renames the administrator account.
func (s *Server) handleUpdateAdminProfile(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var req struct {
		Username string `json:"username"`
	}
	if !s.decodeJSON(w, r, &req) {
		return
	}
	if !validUsername(req.Username) {
		s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "用户名不合法")
		return
	}
	if err := s.store.UpdateAdminUsername(p.SubjectID, req.Username); err != nil {
		s.writeError(w, r, http.StatusConflict, protocol.ErrorCode("USERNAME_TAKEN"), "用户名已存在")
		return
	}
	s.audit(r, "admin.rename", "account", p.SubjectID, req.Username, "")
	writeJSON(w, http.StatusOK, map[string]string{"username": req.Username})
}
