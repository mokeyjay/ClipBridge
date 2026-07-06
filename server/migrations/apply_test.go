package migrations

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite" // 测试用纯 Go sqlite 驱动
)

// TestApplyOnSquashedHistoryDB 复现真实升级场景：历史库在迁移拆分前已应用过
// 0001–0005（schema_migrations 记到 version 5），之后迁移被 squash 进 0001_init.sql。
// 新增迁移必须编号在历史最大版本之上（如 0006），否则会被 `version <= current`
// 跳过——此前 audit_logs 误编号为 0002 正是这样在老库上漏建的。
func TestApplyOnSquashedHistoryDB(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// 1) 老库表结构 = squash 后的 0001（其内容等价于历史 0001–0005 的合集）。
	init0001, err := files.ReadFile("0001_init.sql")
	if err != nil {
		t.Fatalf("read 0001: %v", err)
	}
	if _, err := db.Exec(string(init0001)); err != nil {
		t.Fatalf("exec 0001: %v", err)
	}
	// 2) 老库的 schema_migrations 记录着历史的 1..5 五个版本。
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	for v, name := range map[int]string{
		1: "0001_init.sql", 2: "0002_clipboard_chunks.sql", 3: "0003_clipboard_metadata.sql",
		4: "0004_user_file_ttl.sql", 5: "0005_server_allowed_types.sql",
	} {
		if _, err := db.Exec(`INSERT INTO schema_migrations(version, name, applied_at) VALUES (?,?,0)`, v, name); err != nil {
			t.Fatalf("seed version %d: %v", v, err)
		}
	}

	// 3) 应用当前迁移集：0006 必须在该库上落地 audit_logs。
	if err := Apply(db); err != nil {
		t.Fatalf("apply: %v", err)
	}
	var name string
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='audit_logs'`).Scan(&name); err != nil {
		t.Fatalf("audit_logs 在历史库上未创建: %v", err)
	}
	var cur int
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&cur); err != nil || cur < 6 {
		t.Fatalf("schema version = %d, err %v, want >= 6", cur, err)
	}

	// 4) 防回归约束：任何新迁移文件的编号都必须大于历史最高版本 5，
	//    否则在拆分前的老库上会被静默跳过。
	all, err := load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, m := range all {
		if m.version != 1 && m.version <= 5 {
			t.Errorf("迁移 %s 编号 %d 落在历史已占用区间(2..5)，老库会跳过它", m.name, m.version)
		}
	}
}
