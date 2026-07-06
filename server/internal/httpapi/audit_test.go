package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// auditListResponse 是 /admin/audit-logs 的响应形状。
type auditListResponse struct {
	Logs  []auditLogView `json:"logs"`
	Total int            `json:"total"`
}

// TestAuditLogsRecordedAndListed 验证管理员与用户的关键操作都会落操作日志、
// 列表接口可按主体过滤,且日志中不出现任何密码明文。
func TestAuditLogsRecordedAndListed(t *testing.T) {
	env := newTestEnv(t)
	adminC := env.newClient(t, env.web.URL)
	env.loginAdmin(t, adminC)

	// 管理员操作:建用户 + 改实例设置。
	resp := adminC.do(http.MethodPost, apiPrefix+"/admin/users", protocol.LoginRequest{Username: "alice", Password: "password123"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create user status = %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = adminC.do(http.MethodPatch, apiPrefix+"/admin/settings", map[string]any{"server_name": "剪驿测试"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update settings status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 用户关键操作:登录后修改自己的密码。
	userC := env.newClient(t, env.web.URL)
	resp = userC.do(http.MethodPost, apiPrefix+"/auth/login", protocol.LoginRequest{Username: "alice", Password: "password123"})
	resp.Body.Close()
	resp = userC.do(http.MethodPatch, apiPrefix+"/auth/password", protocol.ChangePasswordRequest{CurrentPassword: "password123", NewPassword: "password456"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("change password status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 全量列表:三类动作都在,倒序(最新在前)。
	resp = adminC.do(http.MethodGet, apiPrefix+"/admin/audit-logs", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", resp.StatusCode)
	}
	var list auditListResponse
	decodeBody(t, resp, &list)
	if list.Total < 3 {
		t.Fatalf("total = %d, want >= 3", list.Total)
	}
	got := map[string]bool{}
	for _, l := range list.Logs {
		got[l.Action] = true
		// 任何条目不得携带密码明文。
		for _, field := range []string{l.Detail, l.TargetName, l.ActorName} {
			if strings.Contains(field, "password123") || strings.Contains(field, "password456") {
				t.Errorf("日志泄漏敏感值: %+v", l)
			}
		}
	}
	for _, want := range []string{"user.create", "settings.update", "account.change_password"} {
		if !got[want] {
			t.Errorf("缺少动作 %s, got %v", want, got)
		}
	}
	if list.Logs[0].Action != "account.change_password" {
		t.Errorf("first = %s, want account.change_password(最新)", list.Logs[0].Action)
	}

	// settings.update 的 detail 记录变更字段名。
	for _, l := range list.Logs {
		if l.Action == "settings.update" && !strings.Contains(l.Detail, "server_name") {
			t.Errorf("settings.update detail = %q, want 含 server_name", l.Detail)
		}
	}

	// 按主体过滤:user 视角只剩用户自己的操作。
	resp = adminC.do(http.MethodGet, apiPrefix+"/admin/audit-logs?actor=user", nil)
	decodeBody(t, resp, &list)
	for _, l := range list.Logs {
		if l.ActorType != "user" {
			t.Errorf("actor 过滤失效: %+v", l)
		}
	}

	// 非管理员不可访问。
	resp = userC.do(http.MethodGet, apiPrefix+"/admin/audit-logs", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("user access status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()
}
