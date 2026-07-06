-- 0006_audit_logs.sql — 操作日志（prd/06 §5、prd/08 §5）。
-- 记录管理员操作与用户关键操作，只存主体/动作/对象与非敏感摘要，
-- 绝不写入密码、token、密钥或剪贴板内容等敏感值。
-- actor/target 的名称在写入时快照，避免对象改名或删除后无法追溯。

CREATE TABLE IF NOT EXISTS audit_logs (
    id          TEXT PRIMARY KEY,
    actor_type  TEXT NOT NULL,             -- admin | user
    actor_id    TEXT NOT NULL,
    actor_name  TEXT NOT NULL DEFAULT '',
    action      TEXT NOT NULL,             -- 如 user.create / device.revoke / settings.update
    target_type TEXT NOT NULL DEFAULT '',  -- user | device | settings | pairing | account
    target_id   TEXT NOT NULL DEFAULT '',
    target_name TEXT NOT NULL DEFAULT '',
    detail      TEXT NOT NULL DEFAULT '',  -- 非敏感摘要（如变更的字段名列表）
    created_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_logs_created ON audit_logs(created_at);
