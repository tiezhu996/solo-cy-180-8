package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/oralhistory/oralhistory/internal/constants"
	"github.com/oralhistory/oralhistory/internal/dto"
	"github.com/oralhistory/oralhistory/internal/middleware"
	"github.com/oralhistory/oralhistory/internal/model"
	"github.com/oralhistory/oralhistory/internal/util"
)

// fakeProjectServiceForHandler 记录调用参数，不连数据库。
type fakeProjectServiceForHandler struct {
	lastListQuery *dto.ProjectListQuery
	lastUpdate    *dto.UpdateProjectRequest
	updateID      uint
	returnProject *model.Project
	err           error
}

func (f *fakeProjectServiceForHandler) Create(actor *model.User, req *dto.CreateProjectRequest) (*model.Project, error) {
	return f.returnProject, f.err
}
func (f *fakeProjectServiceForHandler) Get(id uint) (*model.Project, error) {
	return f.returnProject, f.err
}
func (f *fakeProjectServiceForHandler) List(page, pageSize int, query *dto.ProjectListQuery) ([]model.Project, int64, error) {
	f.lastListQuery = query
	if f.err != nil {
		return nil, 0, f.err
	}
	return []model.Project{}, 0, nil
}
func (f *fakeProjectServiceForHandler) ListMine(actorID uint, page, pageSize int) ([]model.Project, int64, error) {
	return []model.Project{}, 0, nil
}
func (f *fakeProjectServiceForHandler) Update(actor *model.User, id uint, req *dto.UpdateProjectRequest) (*model.Project, error) {
	f.updateID = id
	f.lastUpdate = req
	return f.returnProject, f.err
}
func (f *fakeProjectServiceForHandler) TransitionStatus(actor *model.User, id uint, status string) (*model.Project, error) {
	return f.returnProject, f.err
}
func (f *fakeProjectServiceForHandler) Delete(actor *model.User, id uint) error { return f.err }
func (f *fakeProjectServiceForHandler) Stats() (map[string]any, error)          { return map[string]any{}, nil }

type noopAuditService struct{}

func (noopAuditService) Record(userID uint, username, role, action, entityType string, entityID uint, detail, ip, requestID string) {
}
func (noopAuditService) List(page, pageSize int, username string) ([]model.AuditLog, int64, error) {
	return nil, 0, nil
}

func newProjectRouter(svc *fakeProjectServiceForHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		// 模拟 Auth 中间件写入登录用户。
		c.Set(middleware.ContextKeyClaims, &util.Claims{UserID: 1, Username: "tester", Role: constants.RoleInterviewer})
		c.Next()
	}, middleware.ErrorHandler(slog.Default()))
	h := NewProjectHandler(svc, noopAuditService{}, slog.Default())
	r.GET("/projects", h.List)
	r.PUT("/projects/:id", h.Update)
	return r
}

func decodeBody(t *testing.T, body io.Reader) util.Response {
	t.Helper()
	raw, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var resp util.Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode response %s: %v", string(raw), err)
	}
	return resp
}

// TestProjectHandlerListCombinedQuery 验证 tag 重复键绑定为多标签、关键字与状态组合、分页参数透传。
func TestProjectHandlerListCombinedQuery(t *testing.T) {
	svc := &fakeProjectServiceForHandler{}
	r := newProjectRouter(svc)

	req := httptest.NewRequest(http.MethodGet,
		"/projects?page=2&page_size=10&status=in_progress&tag=%E6%8A%97%E6%88%98&tag=%E7%9F%A5%E9%9D%92&keyword=%20%E7%8E%8B%E5%A5%B6%20", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", w.Code, w.Body.String())
	}
	q := svc.lastListQuery
	if q == nil {
		t.Fatalf("service List not called")
	}
	if q.Page != 2 || q.PageSize != 10 {
		t.Fatalf("page = %d/%d, want 2/10", q.Page, q.PageSize)
	}
	if q.Status != "in_progress" {
		t.Fatalf("status = %q, want in_progress", q.Status)
	}
	if len(q.Tag) != 2 || q.Tag[0] != "抗战" || q.Tag[1] != "知青" {
		t.Fatalf("tags = %v, want [抗战 知青]", q.Tag)
	}
	if q.Keyword != "王奶" {
		t.Fatalf("keyword = %q, want 王奶 (trimmed)", q.Keyword)
	}
}

// TestProjectHandlerListEmptyQuery 空条件：不传任何筛选参数也应 200，且分页归一化。
func TestProjectHandlerListEmptyQuery(t *testing.T) {
	svc := &fakeProjectServiceForHandler{}
	r := newProjectRouter(svc)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/projects", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	q := svc.lastListQuery
	if q.Page != 1 || q.PageSize != 20 || q.Status != "" || len(q.Tag) != 0 || q.Keyword != "" {
		t.Fatalf("empty query = %+v, want normalized zero filter", q)
	}
}

// TestProjectHandlerUpdateTagsPresence 区分编辑请求是否携带 tags 字段。
func TestProjectHandlerUpdateTagsPresence(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantUpdate bool
		wantTags   []string
		wantTitle  string
	}{
		{name: "tags omitted keeps untouched", body: `{"title":"新标题"}`, wantUpdate: false, wantTitle: "新标题"},
		{name: "empty array clears tags", body: `{"tags":[]}`, wantUpdate: true, wantTags: []string{}},
		{name: "tags replaced", body: `{"tags":["抗战","知青"]}`, wantUpdate: true, wantTags: []string{"抗战", "知青"}},
		{name: "tags null clears tags", body: `{"tags":null}`, wantUpdate: true, wantTags: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeProjectServiceForHandler{returnProject: &model.Project{ID: 9, Status: "draft"}}
			r := newProjectRouter(svc)
			req := httptest.NewRequest(http.MethodPut, "/projects/9", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
			}
			if svc.updateID != 9 {
				t.Fatalf("update id = %d, want 9", svc.updateID)
			}
			if svc.lastUpdate.UpdateTags != tc.wantUpdate {
				t.Fatalf("UpdateTags = %v, want %v", svc.lastUpdate.UpdateTags, tc.wantUpdate)
			}
			if tc.wantTitle != "" && *svc.lastUpdate.Title != tc.wantTitle {
				t.Fatalf("title = %q, want %q", *svc.lastUpdate.Title, tc.wantTitle)
			}
			if tc.wantTags != nil && (len(svc.lastUpdate.Tags) != len(tc.wantTags)) {
				t.Fatalf("tags = %v, want %v", svc.lastUpdate.Tags, tc.wantTags)
			}
		})
	}
}

// TestProjectHandlerCreateRejectsOverLimit 通过 service 返回校验错误，验证错误经错误中间件转成 HTTP 400 + code 42200。
func TestProjectHandlerValidationErrorMapping(t *testing.T) {
	svc := &fakeProjectServiceForHandler{
		err: util.NewAppError(constants.CodeValidation, "项目标签最多 5 个，当前去重后为 6 个", nil),
	}
	r := newProjectRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/projects", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("http status = %d, want 400", w.Code)
	}
	resp := decodeBody(t, w.Body)
	if resp.Code != constants.CodeValidation {
		t.Fatalf("business code = %d, want %d", resp.Code, constants.CodeValidation)
	}
}

// TestProjectHandlerArchivedReadOnly 归档项目更新被 service 拒绝时映射为 409 + CodeProjectStatus。
func TestProjectHandlerArchivedReadOnly(t *testing.T) {
	svc := &fakeProjectServiceForHandler{
		err: util.NewAppError(constants.CodeProjectStatus, "项目 7 已归档（状态 archived），标签与基本信息均为只读，禁止修改", nil),
	}
	r := newProjectRouter(svc)

	req := httptest.NewRequest(http.MethodPut, "/projects/7", bytes.NewBufferString(`{"tags":["新标签"]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("http status = %d, want 409", w.Code)
	}
	resp := decodeBody(t, w.Body)
	if resp.Code != constants.CodeProjectStatus {
		t.Fatalf("business code = %d, want %d", resp.Code, constants.CodeProjectStatus)
	}
}
