package store

import "github.com/mokeyjay/clipbridge/shared/protocol"

// EffectiveConfig computes a device's effective sync policy from the three config
// layers (prd/02-architecture.md §7):
//
//	字段选择值 = inherit ? 用户默认 : 设备覆盖
//	最终 max_sync_size = min(服务端实例上限, 字段选择值)   // 真三层
//	allowed_types / auto_upload / auto_download = 字段选择值 // 仅用户+设备两层
//
// Override columns that are unexpectedly NULL fall back to the user default so a
// malformed row can never widen policy.
func (s *Store) EffectiveConfig(deviceID string) (protocol.EffectiveConfig, error) {
	device, err := s.GetDeviceByID(deviceID)
	if err != nil {
		return protocol.EffectiveConfig{}, err
	}
	ss, err := s.GetServerSettings()
	if err != nil {
		return protocol.EffectiveConfig{}, err
	}
	us, err := s.GetUserSettings(device.UserID)
	if err != nil {
		return protocol.EffectiveConfig{}, err
	}
	ds, err := s.GetDeviceSettings(deviceID)
	if err != nil {
		return protocol.EffectiveConfig{}, err
	}

	maxSize := pickInt64(ds.MaxSyncSizeInherit, ds.MaxSyncSizeBytes, us.MaxSyncSizeBytes)
	if ss.MaxSyncSizeBytes < maxSize { // server instance ceiling clamps it
		maxSize = ss.MaxSyncSizeBytes
	}
	types := us.AllowedTypes
	if !ds.AllowedTypesInherit && ds.AllowedTypes != nil {
		types = ds.AllowedTypes
	}
	// 实例级允许列表为硬上限：管理员禁用的类型，用户/设备即便启用也被过滤掉。
	types = intersectTypes(types, ss.AllowedTypes)
	return protocol.EffectiveConfig{
		ServerName:               ss.ServerName,
		MaxSyncSizeBytes:         maxSize,
		AllowedTypes:             types,
		MaxAutoUploadSizeBytes:   pickInt64(ds.MaxAutoUploadInherit, ds.MaxAutoUploadSizeBytes, us.MaxAutoUploadSizeBytes),
		MaxAutoDownloadSizeBytes: pickInt64(ds.MaxAutoDownloadInherit, ds.MaxAutoDownloadSizeBytes, us.MaxAutoDownloadSizeBytes),
		FileTTLDays:              clampTTLDays(us.FileTTLDays),
	}, nil
}

// clampTTLDays keeps the file retention within a sane 1..365 day range so a
// malformed user-level default can't disable cleanup or set an absurd window.
func clampTTLDays(days int64) int64 {
	switch {
	case days < 1:
		return 1
	case days > 365:
		return 365
	default:
		return days
	}
}

// pickInt64 returns the user default when inheriting (or the override is nil),
// otherwise the device override value.
func pickInt64(inherit bool, override *int64, userDefault int64) int64 {
	if inherit || override == nil {
		return userDefault
	}
	return *override
}

// intersectTypes returns the entries of want that are also permitted by the
// instance allowlist, preserving want's order. The instance allowlist is a hard
// ceiling: a type the admin disables can never be re-enabled by a user/device.
func intersectTypes(want, instance []protocol.ContentType) []protocol.ContentType {
	out := make([]protocol.ContentType, 0, len(want))
	for _, t := range want {
		for _, a := range instance {
			if t == a {
				out = append(out, t)
				break
			}
		}
	}
	return out
}
