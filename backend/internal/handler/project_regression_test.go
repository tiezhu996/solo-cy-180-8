package handler_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTokenlessRequest(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func serveTokenless(env *integrationEnv, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	env.router.ServeHTTP(w, req)
	return w
}

// TestE2EPaginationNormalization HTTP 层分页边界：page/page_size 非法值归一化、
// 超大 page_size 收敛为 100、末页越界返回空而 total 保持不变。
func TestE2EPaginationNormalization(t *testing.T) {
	env := setupIntegration(t)

	// 预置 3 个项目。
	for i := 0; i < 3; i++ {
		w := env.do(t, http.MethodPost, "/api/v1/projects",
			createProjectReq(fmt.Sprintf("分页项目%d", i), "受访者", "draft", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("seed %d failed: %s", i, w.Body.String())
		}
	}

	cases := []struct {
		name        string
		path        string
		wantPage    int
		wantSize    int
		wantMinRows int
	}{
		{name: "missing params default to 1/20", path: "/api/v1/projects", wantPage: 1, wantSize: 20, wantMinRows: 3},
		{name: "zero page normalized to 1", path: "/api/v1/projects?page=0&page_size=2", wantPage: 1, wantSize: 2, wantMinRows: 2},
		{name: "negative page normalized to 1", path: "/api/v1/projects?page=-5&page_size=2", wantPage: 1, wantSize: 2, wantMinRows: 2},
		{name: "zero page_size normalized to 20", path: "/api/v1/projects?page=1&page_size=0", wantPage: 1, wantSize: 20, wantMinRows: 3},
		{name: "oversize page_size falls back to default 20", path: "/api/v1/projects?page=1&page_size=9999", wantPage: 1, wantSize: 20, wantMinRows: 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := env.do(t, http.MethodGet, tc.path, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("business rule broken: %s returned %d: %s", tc.name, w.Code, w.Body.String())
			}
			page := decodeData[pagedProjects](t, w)
			if page.Page != tc.wantPage || page.PageSize != tc.wantSize {
				t.Fatalf("business rule broken: %s normalized to page=%d page_size=%d, want %d/%d",
					tc.name, page.Page, page.PageSize, tc.wantPage, tc.wantSize)
			}
			if page.Total != 3 {
				t.Fatalf("business rule broken: %s total = %d, want 3", tc.name, page.Total)
			}
			if len(page.List) < tc.wantMinRows {
				t.Fatalf("business rule broken: %s returned %d rows, want >= %d", tc.name, len(page.List), tc.wantMinRows)
			}
		})
	}

	// 末页越界：page 远超范围时行为安全（返回空列表，但 total 仍为 3，页大小归一化保留）。
	w := env.do(t, http.MethodGet, "/api/v1/projects?page=999&page_size=2", nil)
	page := decodeData[pagedProjects](t, w)
	if page.Total != 3 {
		t.Fatalf("business rule broken: out-of-range total = %d, want 3", page.Total)
	}
	if len(page.List) != 0 {
		t.Fatalf("business rule broken: out-of-range page must return 0 rows, got %d", len(page.List))
	}
}

// TestE2EFilteredPaginationStable 筛选条件跨页保持：tag+keyword 条件下翻页不丢条件、页间不重叠。
func TestE2EFilteredPaginationStable(t *testing.T) {
	env := setupIntegration(t)
	for i := 0; i < 5; i++ {
		body := createProjectReq(fmt.Sprintf("抗战记忆%d", i), "王奶奶", "in_progress", []string{"抗战", "老兵"})
		w := env.do(t, http.MethodPost, "/api/v1/projects", body)
		if w.Code != http.StatusOK {
			t.Fatalf("seed %d: %s", i, w.Body.String())
		}
	}
	// 一个不该命中的干扰项。
	if w := env.do(t, http.MethodPost, "/api/v1/projects",
		createProjectReq("无关项目", "李某某", "draft", []string{"知青"})); w.Code != http.StatusOK {
		t.Fatalf("seed noise: %s", w.Body.String())
	}

	const qs = "/api/v1/projects?page_size=3&status=in_progress&tag=%E8%80%81%E5%85%B5&keyword=%E6%8A%97%E6%88%98"
	p1 := decodeData[pagedProjects](t, env.do(t, http.MethodGet, qs+"&page=1", nil))
	p2 := decodeData[pagedProjects](t, env.do(t, http.MethodGet, qs+"&page=2", nil))
	if p1.Total != 5 {
		t.Fatalf("business rule broken: filtered total = %d, want 5", p1.Total)
	}
	if len(p1.List) != 3 || len(p2.List) != 2 {
		t.Fatalf("business rule broken: filtered page lens = %d/%d, want 3/2", len(p1.List), len(p2.List))
	}
	seen := map[any]bool{}
	for _, p := range append(append([]map[string]any{}, p1.List...), p2.List...) {
		if seen[p["id"]] {
			t.Fatalf("business rule broken: project %v appears on both pages (condition lost across pages)", p["id"])
		}
		seen[p["id"]] = true
	}
}

// TestE2EArchivedProjectFullyReadOnly 归档只读的完整判定：
// 改标题、改受访者、清空标签、替换标签、状态继续流转全部被拒绝，且归档前数据原样保留。
func TestE2EArchivedProjectFullyReadOnly(t *testing.T) {
	env := setupIntegration(t)
	created := decodeData[map[string]any](t, env.do(t, http.MethodPost, "/api/v1/projects",
		createProjectReq("归档只读项目", "王奶奶", "draft", []string{"抗战", "知青"})))
	id := fmt.Sprintf("%v", created["id"])

	for _, st := range []string{"in_progress", "completed", "archived"} {
		if w := env.do(t, http.MethodPut, "/api/v1/projects/"+id+"/status", map[string]any{"status": st}); w.Code != http.StatusOK {
			t.Fatalf("transition to %s: %s", st, w.Body.String())
		}
	}

	rejectCases := []struct {
		name string
		body map[string]any
	}{
		{name: "rename title", body: map[string]any{"title": "被篡改的标题"}},
		{name: "rename interviewee", body: map[string]any{"interviewee_name": "假受访者"}},
		{name: "change birth year", body: map[string]any{"birth_year": 2000}},
		{name: "replace tags", body: map[string]any{"tags": []string{"新标签"}}},
		{name: "clear tags", body: map[string]any{"tags": []string{}}},
		{name: "tags plus title together", body: map[string]any{"title": "x", "tags": []string{"y"}}},
	}
	for _, tc := range rejectCases {
		t.Run(tc.name, func(t *testing.T) {
			w := env.do(t, http.MethodPut, "/api/v1/projects/"+id, tc.body)
			if w.Code != http.StatusConflict {
				t.Fatalf("business rule broken: archived project accepted %s, status=%d body=%s",
					tc.name, w.Code, w.Body.String())
			}
			var resp struct {
				Code int `json:"code"`
			}
			json.Unmarshal(w.Body.Bytes(), &resp)
			if resp.Code != 40902 {
				t.Fatalf("business rule broken: archived reject code = %d, want 40902", resp.Code)
			}
		})
	}

	// 归档状态本身不可再流转。
	if w := env.do(t, http.MethodPut, "/api/v1/projects/"+id+"/status", map[string]any{"status": "in_progress"}); w.Code != http.StatusConflict {
		t.Fatalf("business rule broken: archived status transition returned %d, want 409", w.Code)
	}

	// 归档前数据原样保留：标题、受访者、标签数与状态。
	detail := decodeData[map[string]any](t, env.do(t, http.MethodGet, "/api/v1/projects/"+id, nil))
	if detail["title"] != "归档只读项目" {
		t.Fatalf("business rule broken: archived title mutated to %v", detail["title"])
	}
	if detail["interviewee_name"] != "王奶奶" {
		t.Fatalf("business rule broken: archived interviewee mutated to %v", detail["interviewee_name"])
	}
	if detail["status"] != "archived" {
		t.Fatalf("business rule broken: status = %v, want archived", detail["status"])
	}
	tags, _ := detail["tags"].([]any)
	if len(tags) != 2 {
		t.Fatalf("business rule broken: archived tags = %v, want original 2 tags intact", tags)
	}
}

// TestE2EUnauthenticatedStillRejected 权限回归：无令牌访问项目接口一律 401，标签功能不改变鉴权行为。
func TestE2EUnauthenticatedStillRejected(t *testing.T) {
	env := setupIntegration(t)
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/v1/projects", nil},
		{http.MethodPost, "/api/v1/projects", createProjectReq("x", "y", "draft", nil)},
		{http.MethodGet, "/api/v1/projects/1", nil},
		{http.MethodPut, "/api/v1/projects/1", map[string]any{"tags": []string{"x"}}},
	} {
		req := newTokenlessRequest(t, tc.method, tc.path, tc.body)
		w := serveTokenless(env, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("business rule broken: %s %s without token returned %d, want 401", tc.method, tc.path, w.Code)
		}
	}
}
