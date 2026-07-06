package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/mokeyjay/clipbridge/server/internal/blobstore"
	"github.com/mokeyjay/clipbridge/server/internal/store"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// handleDeviceTargets returns the caller's user's currently online, active
// devices (excluding the caller) with their HPKE public keys, for the source to
// wrap the DEK per target.
func (s *Server) handleDeviceTargets(w http.ResponseWriter, r *http.Request) {
	dev := deviceFrom(r.Context()).device
	online := make(map[string]bool)
	for _, id := range s.hub.OnlineDeviceIDs(dev.UserID) {
		online[id] = true
	}
	devices, err := s.store.ListDevicesByUser(dev.UserID)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "读取目标设备失败")
		return
	}
	targets := make([]protocol.TargetDevice, 0)
	for _, d := range devices {
		if d.ID == dev.ID || d.Status != protocol.DeviceActive || !online[d.ID] {
			continue
		}
		targets = append(targets, protocol.TargetDevice{DeviceID: d.ID, DeviceName: d.Name, HPKEPublicKey: d.HPKEPublicKey})
	}
	writeJSON(w, http.StatusOK, protocol.TargetsResponse{Targets: targets})
}

// handleUploadItem accepts a streamed multipart upload (manifest + ciphertext),
// validates it, persists the item + per-target deliveries, and notifies online
// targets. The body is streamed to disk and never buffered whole in memory.
func (s *Server) handleUploadItem(w http.ResponseWriter, r *http.Request) {
	dev := deviceFrom(r.Context()).device
	mr, err := r.MultipartReader()
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "需要 multipart 请求")
		return
	}

	// First part: the JSON manifest.
	manifest, ok := s.readManifest(w, r, mr)
	if !ok {
		return
	}
	if manifest.ProtocolVersion < protocol.MinSupportedProtocolVersion || manifest.ProtocolVersion > protocol.MaxSupportedProtocolVersion {
		s.writeError(w, r, http.StatusBadRequest, protocol.ErrProtocolVersionUnsupported, "协议版本不受支持")
		return
	}
	if _, err := uuid.Parse(manifest.ItemID); err != nil {
		s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "item_id 不合法")
		return
	}
	if exists, _ := s.store.ItemExists(manifest.ItemID); exists {
		s.writeError(w, r, http.StatusConflict, protocol.ErrorCode("ITEM_ID_IN_USE"), "item_id 已被使用")
		return
	}

	// Defensive policy check against the SOURCE device's effective config: the
	// content type must be permitted and the size within the effective ceiling
	// (which already folds in the server instance limit). prd/02-architecture §7.
	eff, err := s.store.EffectiveConfig(dev.ID)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "求值有效配置失败")
		return
	}
	if !contentTypeAllowed(eff.AllowedTypes, manifest.ContentType) {
		s.writeError(w, r, http.StatusForbidden, protocol.ErrContentTypeNotAllowed, "内容类型不允许")
		return
	}
	maxBytes := eff.MaxSyncSizeBytes
	if manifest.CiphertextSizeBytes > maxBytes {
		s.writeError(w, r, http.StatusRequestEntityTooLarge, protocol.ErrContentTooLarge, "内容超过有效最大尺寸")
		return
	}

	// Validate delivery targets against the live online set before touching disk.
	accepted, acceptedIDs, ok := s.acceptTargets(w, r, dev, manifest.Deliveries)
	if !ok {
		return
	}

	// Second part: stream the ciphertext to disk while hashing.
	part, err := mr.NextPart()
	if err != nil || part.FormName() != "ciphertext" {
		s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "缺少 ciphertext 部分")
		return
	}
	tmp, size, sha, err := s.blobs.WriteIncoming(part, maxBytes)
	if errors.Is(err, blobstore.ErrTooLarge) {
		s.writeError(w, r, http.StatusRequestEntityTooLarge, protocol.ErrContentTooLarge, "内容超过有效最大尺寸")
		return
	}
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "写入密文失败")
		return
	}

	// Integrity: declared size and hash must match the streamed bytes.
	if size != manifest.CiphertextSizeBytes || !strings.EqualFold(sha, manifest.CiphertextSHA256) {
		s.blobs.Abort(tmp)
		s.writeError(w, r, http.StatusBadRequest, protocol.ErrCiphertextIntegrityFailed, "密文完整性校验失败")
		return
	}

	rel, err := s.blobs.Promote(tmp, manifest.ItemID)
	if err != nil {
		s.blobs.Abort(tmp)
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "提交密文失败")
		return
	}

	now := s.store.Now().Unix()
	ttl := int64(300) // fallback; the instance setting governs the real TTL
	if ss, err := s.store.GetServerSettings(); err == nil {
		ttl = ss.CiphertextTTLSeconds
	}
	item := &store.ClipboardItem{
		ID: manifest.ItemID, UserID: dev.UserID, SourceDeviceID: dev.ID, ContentType: manifest.ContentType,
		CiphertextSizeBytes: size, CiphertextPath: rel, CiphertextSHA256: strings.ToLower(sha),
		ChunkSizeBytes: manifest.ChunkSizeBytes, TotalChunks: manifest.TotalChunks,
		EncryptedMetadata: manifest.EncryptedMetadata,
		ExpiresAt:         now + ttl, CreatedAt: now,
	}
	deliveries, err := s.store.CreateItemWithDeliveries(item, accepted)
	if err != nil {
		s.blobs.Remove(rel)
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "创建投递失败")
		return
	}

	// Notify each online target; the client re-fetches authoritative state.
	for _, d := range deliveries {
		s.hub.NotifyDevice(d.TargetDeviceID, protocol.Event{
			Event: protocol.EventDeliveryCreated, OccurredAt: rfc3339(now),
			Data: protocol.DeliveryCreatedData{DeliveryID: d.ID},
		})
	}
	s.logSync(dev.UserID, &item.ID, &dev.ID, nil, "upload_created", string(item.ContentType), &size, "success", nil)

	writeJSON(w, http.StatusOK, protocol.UploadResponse{
		ItemID: item.ID, ExpiresAt: rfc3339(item.ExpiresAt), AcceptedTargetDeviceIDs: acceptedIDs,
	})
}

// readManifest reads and decodes the first multipart part as the JSON manifest.
func (s *Server) readManifest(w http.ResponseWriter, r *http.Request, mr *multipart.Reader) (protocol.UploadManifest, bool) {
	var manifest protocol.UploadManifest
	part, err := mr.NextPart()
	if err != nil || part.FormName() != "manifest" {
		s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "缺少 manifest 部分")
		return manifest, false
	}
	dec := json.NewDecoder(io.LimitReader(part, maxJSONBody))
	if err := dec.Decode(&manifest); err != nil {
		s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "manifest 无法解析")
		return manifest, false
	}
	_ = part.Close()
	return manifest, true
}

// acceptTargets filters the manifest's targets to same-user, active, online
// devices (excluding the source). With no acceptable target it writes
// NO_ONLINE_TARGETS and returns ok=false.
func (s *Server) acceptTargets(w http.ResponseWriter, r *http.Request, source *store.Device, want []protocol.DeliveryTarget) ([]store.NewDeliveryTarget, []string, bool) {
	online := make(map[string]bool)
	for _, id := range s.hub.OnlineDeviceIDs(source.UserID) {
		online[id] = true
	}
	devices, err := s.store.ListDevicesByUser(source.UserID)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "读取设备失败")
		return nil, nil, false
	}
	byID := make(map[string]*store.Device, len(devices))
	for _, d := range devices {
		byID[d.ID] = d
	}
	var accepted []store.NewDeliveryTarget
	var ids []string
	for _, t := range want {
		d := byID[t.TargetDeviceID]
		if d == nil || d.ID == source.ID || d.Status != protocol.DeviceActive || !online[d.ID] {
			continue
		}
		accepted = append(accepted, store.NewDeliveryTarget{TargetDeviceID: t.TargetDeviceID, WrappedDEK: t.WrappedDEK})
		ids = append(ids, t.TargetDeviceID)
	}
	if len(accepted) == 0 {
		s.writeError(w, r, http.StatusConflict, protocol.ErrNoOnlineTargets, "没有在线目标设备")
		return nil, nil, false
	}
	return accepted, ids, true
}

// handlePendingDeliveries lists the caller device's unresolved, unexpired deliveries.
func (s *Server) handlePendingDeliveries(w http.ResponseWriter, r *http.Request) {
	dev := deviceFrom(r.Context()).device
	list, err := s.store.ListPendingDeliveries(dev.ID)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "读取待处理投递失败")
		return
	}
	manifests := make([]protocol.DeliveryManifest, 0, len(list))
	for _, dd := range list {
		manifests = append(manifests, toDeliveryManifest(dd))
	}
	writeJSON(w, http.StatusOK, protocol.PendingDeliveriesResponse{Deliveries: manifests})
}

// handleGetDelivery returns the delivery manifest (with this device's wrapped DEK).
func (s *Server) handleGetDelivery(w http.ResponseWriter, r *http.Request) {
	dd, ok := s.loadServableDelivery(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, toDeliveryManifest(dd))
}

// handleDownloadContent streams the ciphertext for a delivery owned by the caller.
func (s *Server) handleDownloadContent(w http.ResponseWriter, r *http.Request) {
	dd, ok := s.loadServableDelivery(w, r)
	if !ok {
		return
	}
	rc, err := s.blobs.Open(dd.CiphertextPath)
	if err != nil {
		s.writeError(w, r, http.StatusGone, protocol.ErrDeliveryExpired, "密文已不可用")
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(dd.CiphertextSizeBytes, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// loadServableDelivery loads a pending, unexpired delivery owned by the caller.
func (s *Server) loadServableDelivery(w http.ResponseWriter, r *http.Request) (*store.DeliveryDetail, bool) {
	dev := deviceFrom(r.Context()).device
	dd, err := s.store.GetDeliveryForDevice(r.PathValue("id"), dev.ID)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, r, http.StatusNotFound, protocol.ErrorCode("NOT_FOUND"), "投递不存在")
		return nil, false
	}
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "读取投递失败")
		return nil, false
	}
	if dd.ExpiresAt <= s.store.Now().Unix() {
		s.writeError(w, r, http.StatusGone, protocol.ErrDeliveryExpired, "投递已过期")
		return nil, false
	}
	if dd.Status != protocol.DeliveryPending {
		s.writeError(w, r, http.StatusConflict, protocol.ErrorCode("DELIVERY_RESOLVED"), "投递已处理")
		return nil, false
	}
	return dd, true
}

// handleAckDelivery marks a delivery acked, finishing the item if it was the last.
func (s *Server) handleAckDelivery(w http.ResponseWriter, r *http.Request) {
	s.resolveAndRespond(w, r, protocol.DeliveryAcked, nil, "delivery_acked")
}

// handleRejectDelivery marks a delivery rejected with an enumerated reason.
func (s *Server) handleRejectDelivery(w http.ResponseWriter, r *http.Request) {
	var req protocol.RejectRequest
	if !s.decodeJSON(w, r, &req) {
		return
	}
	if !validRejectReason(req.Reason) {
		s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "拒绝原因不合法")
		return
	}
	reason := string(req.Reason)
	s.resolveAndRespond(w, r, protocol.DeliveryRejected, &reason, "delivery_rejected")
}

// resolveAndRespond applies a delivery resolution, deletes the ciphertext and
// notifies the source when the item completes, and writes a sync log.
func (s *Server) resolveAndRespond(w http.ResponseWriter, r *http.Request, status protocol.DeliveryStatus, reason *string, event string) {
	dev := deviceFrom(r.Context()).device
	res, err := s.store.ResolveDelivery(r.PathValue("id"), dev.ID, status, reason)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, r, http.StatusNotFound, protocol.ErrorCode("NOT_FOUND"), "投递不存在或已处理")
		return
	}
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "处理投递失败")
		return
	}
	if res.ItemCompleted {
		s.blobs.Remove(res.CiphertextPath)
		s.hub.NotifyDevice(res.SourceDeviceID, protocol.Event{
			Event: protocol.EventDeliveryResolved, OccurredAt: rfc3339(s.store.Now().Unix()),
			Data: protocol.DeliveryResolvedData{ItemID: res.ItemID, AggregateState: string(protocol.ItemCompleted)},
		})
	}
	s.logSync(dev.UserID, &res.ItemID, nil, &dev.ID, event, "", nil, "success", nil)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// toDeliveryManifest projects a joined delivery+item into the wire manifest.
func toDeliveryManifest(dd *store.DeliveryDetail) protocol.DeliveryManifest {
	return protocol.DeliveryManifest{
		DeliveryID: dd.ID, ItemID: dd.ClipboardItemID, SourceDeviceID: dd.SourceDeviceID,
		ContentType: dd.ContentType, CiphertextSizeBytes: dd.CiphertextSizeBytes, CiphertextSHA256: dd.CiphertextSHA256,
		ChunkSizeBytes: dd.ChunkSizeBytes, TotalChunks: dd.TotalChunks, WrappedDEK: dd.WrappedDEK,
		EncryptedMetadata: dd.EncryptedMetadata, ExpiresAt: rfc3339(dd.ExpiresAt),
	}
}

// validRejectReason reports whether reason is one of the enumerated values.
func validRejectReason(reason protocol.RejectReason) bool {
	switch reason {
	case protocol.RejectUserDeclined, protocol.RejectConfirmationTimeout, protocol.RejectPolicyBlocked,
		protocol.RejectDecryptFailed, protocol.RejectKeyFingerprintMismatch:
		return true
	}
	return false
}

// logSync writes a minimal sync log row, swallowing errors (logging must never
// break the request path).
func (s *Server) logSync(userID string, itemID, srcDev, tgtDev *string, event, contentType string, size *int64, result string, errorCode *string) {
	var ct *string
	if contentType != "" {
		ct = &contentType
	}
	_ = s.store.InsertSyncLog(&store.SyncLog{
		UserID: userID, ItemID: itemID, SourceDeviceID: srcDev, TargetDeviceID: tgtDev,
		EventType: event, ContentType: ct, CiphertextSizeBytes: size, Result: result, ErrorCode: errorCode,
	})
}
