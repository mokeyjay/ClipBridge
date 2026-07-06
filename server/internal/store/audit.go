package store

// 操作日志（audit log）数据访问：记录管理员操作与用户关键操作的最小事实
// （谁、何时、对谁做了什么），供管理端排障与审计（prd/06 §5、prd/08 §5）。
// 约定：任何敏感值（密码、token、密钥、剪贴板内容）都不允许进入本表。

// AuditLog 是一条操作日志。名称字段在写入时快照，避免对象改名/删除后失联。
type AuditLog struct {
	ID         string
	ActorType  string // admin | user
	ActorID    string
	ActorName  string
	Action     string // 如 user.create / device.revoke / settings.update
	TargetType string // user | device | settings | pairing | account；可为空
	TargetID   string
	TargetName string
	Detail     string // 非敏感摘要（如变更字段名列表）
	CreatedAt  int64
}

// InsertAuditLog 追加一条操作日志；ID 与时间由 store 生成。
func (s *Store) InsertAuditLog(l *AuditLog) error {
	_, err := s.db.Exec(
		`INSERT INTO audit_logs(id, actor_type, actor_id, actor_name, action,
		        target_type, target_id, target_name, detail, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		newID(), l.ActorType, l.ActorID, l.ActorName, l.Action,
		l.TargetType, l.TargetID, l.TargetName, l.Detail, s.nowUnix(),
	)
	return err
}

// ListAuditLogsFiltered 按时间倒序分页返回操作日志，可按主体类型
// （admin|user|空）过滤，并用 q 对主体名/动作/对象名做模糊匹配；
// 同时返回匹配总数供分页。
func (s *Store) ListAuditLogsFiltered(actorType, q string, limit, offset int) ([]*AuditLog, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	where := "1=1"
	var args []any
	if actorType == "admin" || actorType == "user" {
		where += " AND actor_type = ?"
		args = append(args, actorType)
	}
	if q != "" {
		like := "%" + q + "%"
		where += " AND (actor_name LIKE ? OR action LIKE ? OR target_name LIKE ? OR detail LIKE ?)"
		args = append(args, like, like, like, like)
	}

	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM audit_logs WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.db.Query(
		`SELECT id, actor_type, actor_id, actor_name, action, target_type, target_id, target_name, detail, created_at
		 FROM audit_logs WHERE `+where+` ORDER BY created_at DESC, rowid DESC LIMIT ? OFFSET ?`,
		append(args, limit, offset)...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*AuditLog
	for rows.Next() {
		l := &AuditLog{}
		if err := rows.Scan(&l.ID, &l.ActorType, &l.ActorID, &l.ActorName, &l.Action,
			&l.TargetType, &l.TargetID, &l.TargetName, &l.Detail, &l.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, l)
	}
	return out, total, rows.Err()
}

// PruneLogs 删除早于保留期的同步日志与操作日志，返回删除总数。
// 两类日志共用「同步日志保留天数」实例配置（最小保留期语义）。
func (s *Store) PruneLogs(retentionDays int64) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	cutoff := s.nowUnix() - retentionDays*24*60*60
	var total int64
	for _, table := range []string{"sync_logs", "audit_logs"} {
		res, err := s.db.Exec(`DELETE FROM `+table+` WHERE created_at < ?`, cutoff)
		if err != nil {
			return total, err
		}
		if n, err := res.RowsAffected(); err == nil {
			total += n
		}
	}
	return total, nil
}
