package store

import (
	"path/filepath"
	"testing"
	"time"
)

// newAuditTestStore 打开一个临时库并返回可控时钟的 Store。
func newAuditTestStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st := New(db)
	now := time.Unix(1_700_000_000, 0)
	st.now = func() time.Time { return now }
	return st, &now
}

// TestAuditInsertListFilter 验证操作日志的写入、倒序分页与过滤。
func TestAuditInsertListFilter(t *testing.T) {
	st, now := newAuditTestStore(t)

	// 依次写入三条：admin 两条、user 一条，时间递增。
	entries := []*AuditLog{
		{ActorType: "admin", ActorID: "a1", ActorName: "root", Action: "user.create", TargetType: "user", TargetName: "alice"},
		{ActorType: "user", ActorID: "u1", ActorName: "alice", Action: "device.revoke", TargetType: "device", TargetName: "MacBook"},
		{ActorType: "admin", ActorID: "a1", ActorName: "root", Action: "settings.update", TargetType: "settings", Detail: "max_sync_size_bytes"},
	}
	for _, e := range entries {
		if err := st.InsertAuditLog(e); err != nil {
			t.Fatalf("insert: %v", err)
		}
		*now = now.Add(time.Second)
	}

	// 全量倒序：最后写入的排最前。
	logs, total, err := st.ListAuditLogsFiltered("", "", 20, 0)
	if err != nil || total != 3 || len(logs) != 3 {
		t.Fatalf("list all = %d/%d, err %v", len(logs), total, err)
	}
	if logs[0].Action != "settings.update" || logs[2].Action != "user.create" {
		t.Errorf("排序错误: %s / %s", logs[0].Action, logs[2].Action)
	}

	// 按主体类型过滤。
	logs, total, err = st.ListAuditLogsFiltered("user", "", 20, 0)
	if err != nil || total != 1 || logs[0].ActorName != "alice" {
		t.Fatalf("filter user = %+v total=%d err=%v", logs, total, err)
	}

	// 模糊搜索命中对象名。
	logs, total, err = st.ListAuditLogsFiltered("", "MacBook", 20, 0)
	if err != nil || total != 1 || logs[0].Action != "device.revoke" {
		t.Fatalf("search = %+v total=%d err=%v", logs, total, err)
	}
}

// TestPruneLogs 验证同步日志与操作日志按保留天数一起修剪，0 表示不修剪。
func TestPruneLogs(t *testing.T) {
	st, now := newAuditTestStore(t)

	// 一条旧操作日志 + 一条旧同步日志。
	if err := st.InsertAuditLog(&AuditLog{ActorType: "admin", ActorID: "a1", Action: "user.create"}); err != nil {
		t.Fatalf("insert audit: %v", err)
	}
	if err := st.InsertSyncLog(&SyncLog{UserID: "u1", EventType: "upload", Result: "success"}); err != nil {
		t.Fatalf("insert sync: %v", err)
	}

	// 时间快进 40 天后再写一条新操作日志。
	*now = now.Add(40 * 24 * time.Hour)
	if err := st.InsertAuditLog(&AuditLog{ActorType: "admin", ActorID: "a1", Action: "user.disable"}); err != nil {
		t.Fatalf("insert audit new: %v", err)
	}

	// 保留 0 天 = 永久保留，不得删除任何行。
	if n, err := st.PruneLogs(0); err != nil || n != 0 {
		t.Fatalf("prune 0 = %d, err %v", n, err)
	}

	// 保留 30 天:旧的两条（audit+sync）被删,新的保留。
	n, err := st.PruneLogs(30)
	if err != nil || n != 2 {
		t.Fatalf("prune 30 = %d, err %v", n, err)
	}
	logs, total, err := st.ListAuditLogsFiltered("", "", 20, 0)
	if err != nil || total != 1 || logs[0].Action != "user.disable" {
		t.Fatalf("剩余日志错误: %+v total=%d err=%v", logs, total, err)
	}
}
