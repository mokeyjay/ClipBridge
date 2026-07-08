// Package updatecheck 通过 GitHub Releases API 检查客户端是否有新的正式版本。
// 只关注正式版：任何版本号带 `-`（如 -beta.1 / -alpha.3）的预发布版本一律忽略。
// 网络失败为非致命，调用方按「暂无更新」处理即可。
package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// releasesLatestURL 是 GitHub 「最新正式版」接口：它天然排除草稿与预发布版，
// 返回按时间最新的一个正式 Release。
const releasesLatestURL = "https://api.github.com/repos/mokeyjay/ClipBridge/releases/latest"

// Release 是一次检查得到的正式版本信息。
type Release struct {
	TagName string // 版本标签，如 v1.2.0
	HTMLURL string // 对应版本的 release 页面地址（点击跳转用）
}

// ghRelease 是 GitHub API 响应中我们关心的字段。
type ghRelease struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// Latest 拉取仓库的最新正式版本。带 `-` 的预发布标签（防御性双保险，正常情况下
// 该接口不会返回预发布版）会被当作「无正式版」返回 nil。
func Latest(ctx context.Context) (*Release, error) {
	return latestFrom(ctx, releasesLatestURL)
}

// latestFrom 从指定 URL 拉取并解析最新正式版本，供 Latest 与单测复用。
func latestFrom(ctx context.Context, url string) (*Release, error) {
	// 独立的短超时 HTTP 客户端：更新检查不应长时间占用或阻塞。
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// GitHub API 要求带 User-Agent，否则返回 403；Accept 指定 v3 JSON。
	req.Header.Set("User-Agent", "ClipBridge-UpdateCheck")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// 仓库尚无正式 Release 时该接口返回 404；视为「无更新」而非错误。
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github releases 接口返回 %d", resp.StatusCode)
	}

	var r ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	// 预发布 / 草稿 / 标签带 `-` 的一律不作为正式版。
	if r.Draft || r.Prerelease || strings.Contains(r.TagName, "-") || r.TagName == "" {
		return nil, nil
	}
	return &Release{TagName: r.TagName, HTMLURL: r.HTMLURL}, nil
}

// IsNewer 判断 latest 是否为比 current 更新的正式版本。两者都做宽松解析
// （容忍前导 v、缺失的段位、非数字段位），按点分数字段逐段比较；基础版本号
// 相同时，正式版（current 带 `-` 的预发布）视为更新。
func IsNewer(latest, current string) bool {
	lv, lpre := parseVersion(latest)
	cv, cpre := parseVersion(current)
	n := len(lv)
	if len(cv) > n {
		n = len(cv)
	}
	for i := 0; i < n; i++ {
		a, b := 0, 0
		if i < len(lv) {
			a = lv[i]
		}
		if i < len(cv) {
			b = cv[i]
		}
		if a != b {
			return a > b
		}
	}
	// 数字段位完全相同：正式版胜过同基版本的预发布版（如 v1.0.0 > v1.0.0-beta.1）。
	return !lpre && cpre
}

// parseVersion 把版本字符串拆成数字段位切片，并返回它是否为预发布版。
// 去掉前导 v/V，`-` 之后为预发布标识（返回 pre=true 且不参与数字比较），
// 每段非数字前缀取其中的数字，无法解析的段位记为 0。
func parseVersion(s string) (nums []int, pre bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	if i := strings.IndexByte(s, '-'); i >= 0 {
		pre = true
		s = s[:i]
	}
	if s == "" {
		return nil, pre
	}
	for _, part := range strings.Split(s, ".") {
		nums = append(nums, leadingInt(part))
	}
	return nums, pre
}

// leadingInt 取字符串开头的连续数字并转为整数；没有数字则返回 0。
func leadingInt(s string) int {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	n, _ := strconv.Atoi(s[:end])
	return n
}
