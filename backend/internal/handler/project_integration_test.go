package handler_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/oralhistory/oralhistory/internal/constants"
	"github.com/oralhistory/oralhistory/internal/database"
	"github.com/oralhistory/oralhistory/internal/handler"
	"github.com/oralhistory/oralhistory/internal/middleware"
	"github.com/oralhistory/oralhistory/internal/repository"
	"github.com/oralhistory/oralhistory/internal/service"
	"github.com/oralhistory/oralhistory/internal/util"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// integrationEnv 真实 repository + service + handler，使用 SQLite 内存库跑端到端链路。
type integrationEnv struct {
	router http.Handler
	db     *gorm.DB
	token  string
}

func setupIntegration(t *testing.T) *integrationEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	// 每个测试独立内存库（DSN 带测试名，避免共享缓存串数据）。
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	projectRepo := repository.NewProjectRepository(db)
	projectSvc := service.NewProjectService(projectRepo, logger)
	auditRepo := repository.NewAuditLogRepository(db)
	auditSvc := service.NewAuditService(auditRepo, logger)
	projectHandler := handler.NewProjectHandler(projectSvc, auditSvc, logger)

	secret := "integration-test-secret-key"
	r := gin.New()
	r.Use(middleware.ErrorHandler(logger))
	authed := r.Group("/api/v1", middleware.Auth(secret, logger))
	authed.GET("/projects", projectHandler.List)
	authed.POST("/projects", projectHandler.Create)
	authed.PUT("/projects/:id", projectHandler.Update)
	authed.PUT("/projects/:id/status", projectHandler.TransitionStatus)
	authed.GET("/projects/:id", projectHandler.Get)

	token, err := util.GenerateToken(1, "tester", constants.RoleInterviewer, secret, 24)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	return &integrationEnv{router: r, db: db, token: token}
}

func (e *integrationEnv) do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+e.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

type pagedProjects struct {
	List     []map[string]any `json:"list"`
	Total    int64            `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
}

func decodeData[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var env struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    T      `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	if env.Code != 0 {
		t.Fatalf("unexpected business code %d: %s", env.Code, env.Message)
	}
	return env.Data
}

func createProjectReq(title, interviewee, status string, tags []string) map[string]any {
	return map[string]any{
		"title":            title,
		"interviewee_name": interviewee,
		"birth_year":       1938,
		"background":       "集成测试",
		"status":           status,
		"tags":             tags,
	}
}

// TestIntegrationCreateProjectWithTags 创建带标签项目（空环境建表后的核心回归）。
func TestIntegrationCreateProjectWithTags(t *testing.T) {
	env := setupIntegration(t)

	// 未认证必须 401，确认权限链路未受影响。
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", nil)
	env.router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", w.Code)
	}

	w = env.do(t, http.MethodPost, "/api/v1/projects",
		createProjectReq("老城记忆", "王奶奶", "draft", []string{"抗战", "知青", "抗战", " 女工 "}))
	if w.Code != http.StatusOK {
		t.Fatalf("create with tags: status = %d body=%s", w.Code, w.Body.String())
	}
	project := decodeData[map[string]any](t, w)
	tags, _ := project["tags"].([]any)
	if len(tags) != 3 {
		t.Fatalf("tags after dedup = %v, want 3 items", tags)
	}
	gotTags := []string{tags[0].(string), tags[1].(string), tags[2].(string)}
	want := []string{"抗战", "知青", "女工"}
	for i := range want {
		if gotTags[i] != want[i] {
			t.Fatalf("tags = %v, want %v", gotTags, want)
		}
	}

	// 详情接口应返回相同标签。
	w = env.do(t, http.MethodGet, fmt.Sprintf("/api/v1/projects/%v", project["id"]), nil)
	detail := decodeData[map[string]any](t, w)
	if dtags, _ := detail["tags"].([]any); len(dtags) != 3 {
		t.Fatalf("detail tags = %v, want 3", dtags)
	}
}

// TestIntegrationCreateWithoutTagsLegacyShape 旧行为：不带标签创建，tags 为空数组而非 null。
func TestIntegrationCreateWithoutTagsLegacyShape(t *testing.T) {
	env := setupIntegration(t)
	body := map[string]any{
		"title": "无标签项目", "interviewee_name": "张爷爷", "birth_year": 1931,
	}
	w := env.do(t, http.MethodPost, "/api/v1/projects", body)
	if w.Code != http.StatusOK {
		t.Fatalf("create: %s", w.Body.String())
	}
	project := decodeData[map[string]any](t, w)
	rawTags, ok := project["tags"]
	if !ok {
		t.Fatalf("tags field missing")
	}
	arr, ok := rawTags.([]any)
	if !ok || len(arr) != 0 {
		t.Fatalf("tags = %v, want empty array (legacy compatibility)", rawTags)
	}
}

// TestIntegrationCreateValidationRejects 空标签与超限明确拒绝（HTTP 400，code 42200），不落库。
func TestIntegrationCreateValidationRejects(t *testing.T) {
	env := setupIntegration(t)
	cases := []struct {
		name string
		tags []string
	}{
		{name: "empty element", tags: []string{"抗战", "  "}},
		{name: "over limit", tags: []string{"a", "b", "c", "d", "e", "f"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := env.do(t, http.MethodPost, "/api/v1/projects",
				createProjectReq("项目"+tc.name, "受访者", "draft", tc.tags))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 body=%s", w.Code, w.Body.String())
			}
			var envBody struct {
				Code int `json:"code"`
			}
			json.Unmarshal(w.Body.Bytes(), &envBody)
			if envBody.Code != constants.CodeValidation {
				t.Fatalf("business code = %d, want %d", envBody.Code, constants.CodeValidation)
			}
		})
	}
	var count int64
	if err := env.db.Raw("SELECT count(*) FROM projects").Scan(&count).Error; err != nil {
		t.Fatalf("count projects: %v", err)
	}
	if count != 0 {
		t.Fatalf("rejected requests must not persist, projects = %d", count)
	}
}

// TestIntegrationCombinedFilterAndPagination 状态 + 多标签 AND + 关键字组合筛选，并验证分页。
func TestIntegrationCombinedFilterAndPagination(t *testing.T) {
	env := setupIntegration(t)

	seed := []struct {
		title, who, status string
		tags               []string
	}{
		{"王奶奶的知青岁月", "王奶奶", "in_progress", []string{"抗战", "知青"}},
		{"王爷爷抗战纪实", "王爷爷", "in_progress", []string{"抗战", "老兵"}},
		{"纺织厂女工口述", "李奶奶", "completed", []string{"知青", "女工"}},
		{"普通项目", "赵叔叔", "draft", nil},
	}
	for _, s := range seed {
		w := env.do(t, http.MethodPost, "/api/v1/projects", createProjectReq(s.title, s.who, s.status, s.tags))
		if w.Code != http.StatusOK {
			t.Fatalf("seed %s: %s", s.title, w.Body.String())
		}
	}

	// 空条件：全部 4 条。
	w := env.do(t, http.MethodGet, "/api/v1/projects?page=1&page_size=20", nil)
	page := decodeData[pagedProjects](t, w)
	if page.Total != 4 {
		t.Fatalf("empty condition total = %d, want 4", page.Total)
	}

	// 单状态筛选。
	w = env.do(t, http.MethodGet, "/api/v1/projects?status=in_progress", nil)
	page = decodeData[pagedProjects](t, w)
	if page.Total != 2 {
		t.Fatalf("status filter total = %d, want 2", page.Total)
	}

	// 组合：in_progress + 双标签 AND + 关键字“王”。
	w = env.do(t, http.MethodGet,
		"/api/v1/projects?page=1&page_size=10&status=in_progress&tag=%E6%8A%97%E6%88%98&tag=%E7%9F%A5%E9%9D%92&keyword=%E7%8E%8B", nil)
	page = decodeData[pagedProjects](t, w)
	if page.Total != 1 || len(page.List) != 1 {
		t.Fatalf("combined filter total=%d len=%d, want 1/1", page.Total, len(page.List))
	}
	if page.List[0]["title"] != "王奶奶的知青岁月" {
		t.Fatalf("combined filter hit = %v", page.List[0]["title"])
	}

	// 关键字只命中受访者姓名的场景。
	w = env.do(t, http.MethodGet, "/api/v1/projects?keyword=%E7%88%B7%E7%88%B7", nil) // keyword=爷爷
	page = decodeData[pagedProjects](t, w)
	if page.Total != 1 || page.List[0]["interviewee_name"] != "王爷爷" {
		t.Fatalf("interviewee keyword search total=%d, want 王爷爷", page.Total)
	}

	// 分页：page_size=2，第 1 页 2 条、第 2 页 2 条，total 仍为 4。
	w = env.do(t, http.MethodGet, "/api/v1/projects?page=1&page_size=2", nil)
	page1 := decodeData[pagedProjects](t, w)
	w = env.do(t, http.MethodGet, "/api/v1/projects?page=2&page_size=2", nil)
	page2 := decodeData[pagedProjects](t, w)
	if page1.Total != 4 || len(page1.List) != 2 || page2.PageSize == 0 || len(page2.List) != 2 {
		t.Fatalf("pagination broken: page1=%d total=%d, page2=%d", len(page1.List), page1.Total, len(page2.List))
	}
	if page1.List[0]["id"] == page2.List[0]["id"] {
		t.Fatalf("pages overlap")
	}
}

// TestIntegrationEditTagsAndArchivedReadonly 编辑替换/合并/保留标签；归档后只读。
func TestIntegrationEditTagsAndArchivedReadonly(t *testing.T) {
	env := setupIntegration(t)

	w := env.do(t, http.MethodPost, "/api/v1/projects",
		createProjectReq("待编辑项目", "钱奶奶", "draft", []string{"旧标签"}))
	project := decodeData[map[string]any](t, w)
	id := fmt.Sprintf("%v", project["id"])

	// 编辑：替换标签，重复输入自动合并；只传 tags 不动其他字段。
	w = env.do(t, http.MethodPut, "/api/v1/projects/"+id,
		map[string]any{"tags": []string{"新标签", "新标签", "抗战"}})
	if w.Code != http.StatusOK {
		t.Fatalf("update tags: status=%d body=%s", w.Code, w.Body.String())
	}
	updated := decodeData[map[string]any](t, w)
	tags, _ := updated["tags"].([]any)
	if len(tags) != 2 {
		t.Fatalf("updated tags = %v, want 2 merged", tags)
	}
	if updated["title"] != "待编辑项目" {
		t.Fatalf("title should stay unchanged, got %v", updated["title"])
	}

	// 不带 tags 字段时原标签保留。
	w = env.do(t, http.MethodPut, "/api/v1/projects/"+id, map[string]any{"background": "更新简介"})
	updated = decodeData[map[string]any](t, w)
	if tags, _ := updated["tags"].([]any); len(tags) != 2 {
		t.Fatalf("tags = %v, should be kept when field omitted", tags)
	}

	// 空数组清空标签。
	w = env.do(t, http.MethodPut, "/api/v1/projects/"+id, map[string]any{"tags": []string{}})
	updated = decodeData[map[string]any](t, w)
	if tags, _ := updated["tags"].([]any); len(tags) != 0 {
		t.Fatalf("tags = %v, want cleared", tags)
	}

	// 流转到归档：draft -> in_progress -> completed -> archived。
	for _, st := range []string{"in_progress", "completed", "archived"} {
		w = env.do(t, http.MethodPut, "/api/v1/projects/"+id+"/status", map[string]any{"status": st})
		if w.Code != http.StatusOK {
			t.Fatalf("transition to %s: %s", st, w.Body.String())
		}
	}

	// 归档后修改基本信息被拒绝（409，code 40902）。
	w = env.do(t, http.MethodPut, "/api/v1/projects/"+id, map[string]any{"title": "强行改名"})
	if w.Code != http.StatusConflict {
		t.Fatalf("archived update status = %d, want 409", w.Code)
	}
	var envBody struct{ Code int }
	json.Unmarshal(w.Body.Bytes(), &envBody)
	if envBody.Code != constants.CodeProjectStatus {
		t.Fatalf("archived code = %d, want %d", envBody.Code, constants.CodeProjectStatus)
	}

	// 归档后改标签同样被拒绝。
	w = env.do(t, http.MethodPut, "/api/v1/projects/"+id, map[string]any{"tags": []string{"想加标签"}})
	if w.Code != http.StatusConflict {
		t.Fatalf("archived tag update status = %d, want 409", w.Code)
	}

	// 数据未被改动：标题保持原样、标签仍为空。
	w = env.do(t, http.MethodGet, "/api/v1/projects/"+id, nil)
	detail := decodeData[map[string]any](t, w)
	if detail["title"] != "待编辑项目" {
		t.Fatalf("archived title mutated = %v", detail["title"])
	}
	if tags, _ := detail["tags"].([]any); len(tags) != 0 {
		t.Fatalf("archived tags mutated = %v", tags)
	}
}
