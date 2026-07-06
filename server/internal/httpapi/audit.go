package httpapi

// 操作日志的写入辅助与管理端查询接口（prd/06 §5、prd/08 §5）。
// 写入是 best-effort：审计失败不阻塞业务操作本身；任何敏感值不得进入日志。

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/mokeyjay/clipbridge/server/internal/store"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// audit 以当前请求的登录主体为 actor 写入一条不含敏感值的操作日志。
// detail 只允许放非敏感摘要（如变更的字段名列表），绝不放具体口令/token。
func (s *Server) audit(r *http.Request, action, targetType, targetID, targetName, detail string) {
	p := principalFrom(r.Context())
	if p == nil {
		return
	}
	_ = s.store.InsertAuditLog(&store.AuditLog{
		ActorType: string(p.SubjectType), ActorID: p.SubjectID, ActorName: p.Username,
		Action: action, TargetType: targetType, TargetID: targetID, TargetName: targetName,
		Detail: detail,
	})
}

// auditLogView 是操作日志在管理控制台中的展示形态。
type auditLogView struct {
	ID         string `json:"id"`
	ActorType  string `json:"actor_type"`
	ActorName  string `json:"actor_name"`
	Action     string `json:"action"`
	TargetType string `json:"target_type,omitempty"`
	TargetName string `json:"target_name,omitempty"`
	Detail     string `json:"detail,omitempty"`
	CreatedAt  string `json:"created_at"`
}

// handleListAuditLogs 分页返回操作日志，支持 ?actor=admin|user 过滤与 ?q= 模糊
// 搜索，分页参数与同步日志接口一致（?page= 1 起、每页 20 条）。
func (s *Server) handleListAuditLogs(w http.ResponseWriter, r *http.Request) {
	const pageSize = 20
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	actor := r.URL.Query().Get("actor")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	logs, total, err := s.store.ListAuditLogsFiltered(actor, q, pageSize, (page-1)*pageSize)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "读取操作日志失败")
		return
	}
	views := make([]auditLogView, 0, len(logs))
	for _, l := range logs {
		views = append(views, auditLogView{
			ID: l.ID, ActorType: l.ActorType, ActorName: l.ActorName, Action: l.Action,
			TargetType: l.TargetType, TargetName: l.TargetName, Detail: l.Detail,
			CreatedAt: rfc3339(l.CreatedAt),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"logs": views, "total": total, "page": page, "page_size": pageSize,
	})
}
