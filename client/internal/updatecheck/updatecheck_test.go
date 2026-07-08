package updatecheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestLatestFrom 覆盖 Latest 的解析与过滤逻辑：正式版正常返回，预发布 / 草稿 /
// 带 `-` 的标签视为无更新（nil），404 视为「暂无正式版」（nil）。
func TestLatestFrom(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantTag string
		wantNil bool
	}{
		{"正式版", 200, `{"tag_name":"v1.2.0","html_url":"https://x/releases/tag/v1.2.0"}`, "v1.2.0", false},
		{"预发布标记", 200, `{"tag_name":"v1.2.0","prerelease":true}`, "", true},
		{"草稿", 200, `{"tag_name":"v1.2.0","draft":true}`, "", true},
		{"标签带短横线", 200, `{"tag_name":"v1.2.0-beta.1"}`, "", true},
		{"无正式版404", 404, `{"message":"Not Found"}`, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()
			rel, err := latestFrom(context.Background(), srv.URL)
			if err != nil {
				t.Fatalf("latestFrom 返回错误: %v", err)
			}
			if c.wantNil {
				if rel != nil {
					t.Fatalf("期望 nil，实得 %+v", rel)
				}
				return
			}
			if rel == nil || rel.TagName != c.wantTag {
				t.Fatalf("期望标签 %q，实得 %+v", c.wantTag, rel)
			}
		})
	}
}

// TestIsNewer 覆盖版本比较的关键场景：更高段位、前导 v、缺段、以及
// 同基版本下正式版应胜过预发布版。
func TestIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v1.2.0", "v1.1.0", true},        // 次版本更高
		{"v1.2.1", "v1.2.0", true},        // 修订更高
		{"v2.0.0", "v1.9.9", true},        // 主版本更高
		{"v1.2.0", "v1.2.0", false},       // 完全相同
		{"v1.2.0", "v1.3.0", false},       // current 更高
		{"1.2.0", "v1.2.0", false},        // 有无前导 v 不影响
		{"v1.2", "v1.1.9", true},          // latest 缺段仍更高
		{"v1.2.0", "v1.2", false},         // 补零段位相等
		{"v1.0.0", "v1.0.0-beta.1", true}, // 正式版胜过同基预发布
		{"v1.0.0", "v0.9.0-beta.1", true}, // 更高且对方为预发布
		{"v1.0.0-beta", "v1.0.0", false},  // latest 是预发布（不应发生，防御）
	}
	for _, c := range cases {
		if got := IsNewer(c.latest, c.current); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}
