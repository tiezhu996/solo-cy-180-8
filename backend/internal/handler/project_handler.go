package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/oralhistory/oralhistory/internal/constants"
	"github.com/oralhistory/oralhistory/internal/dto"
	"github.com/oralhistory/oralhistory/internal/middleware"
	"github.com/oralhistory/oralhistory/internal/service"
	"github.com/oralhistory/oralhistory/internal/util"
)

// bindUpdateProject 绑定编辑请求，并探测 body 中是否显式携带 tags 字段：
// 缺省 tags 时保留原标签；tags 为空数组/null 时清空标签。
func bindUpdateProject(c *gin.Context, req *dto.UpdateProjectRequest) bool {
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, constants.CodeValidation, constants.MsgInvalidBody)
		return false
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		util.Fail(c, http.StatusBadRequest, constants.CodeValidation, fmt.Sprintf("%s: %v", constants.MsgValidationFailed, err))
		return false
	}
	if _, ok := probe["tags"]; ok {
		req.UpdateTags = true
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	return bindJSON(c, req)
}

// ProjectHandler 采访项目接口处理器。
type ProjectHandler struct {
	projectSvc service.ProjectService
	auditSvc   service.AuditService
	logger     *slog.Logger
}

// NewProjectHandler 构造项目处理器。
func NewProjectHandler(projectSvc service.ProjectService, auditSvc service.AuditService, logger *slog.Logger) *ProjectHandler {
	return &ProjectHandler{projectSvc: projectSvc, auditSvc: auditSvc, logger: logger}
}

// Create 创建采访项目。
func (h *ProjectHandler) Create(c *gin.Context) {
	actor, err := middleware.CurrentUser(c)
	if err != nil {
		c.Error(err)
		return
	}
	var req dto.CreateProjectRequest
	if !bindJSON(c, &req) {
		return
	}
	project, err := h.projectSvc.Create(actor, &req)
	if err != nil {
		c.Error(err)
		return
	}
	h.auditSvc.Record(actor.ID, actor.Username, actor.Role, "project.create", "project", project.ID,
		"创建采访项目 "+project.Title, c.ClientIP(), middleware.RequestID(c))
	util.OKMessage(c, constants.MsgProjectCreated, project)
}

// Get 查询项目详情。
func (h *ProjectHandler) Get(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	project, err := h.projectSvc.Get(id)
	if err != nil {
		c.Error(err)
		return
	}
	util.OK(c, project)
}

// List 项目列表（状态/标签/关键字可组合筛选，保持分页）。
func (h *ProjectHandler) List(c *gin.Context) {
	var q dto.ProjectListQuery
	if !bindQuery(c, &q) {
		return
	}
	q.Normalize()
	projects, total, err := h.projectSvc.List(q.Page, q.PageSize, &q)
	if err != nil {
		c.Error(err)
		return
	}
	h.logger.Info(fmt.Sprintf(constants.LogProjectListSearch, q.Page, q.PageSize, q.Status, len(q.Tag), q.Keyword))
	util.OK(c, gin.H{"list": projects, "total": total, "page": q.Page, "page_size": q.PageSize})
}

// ListMine 我的项目列表。
func (h *ProjectHandler) ListMine(c *gin.Context) {
	actor, err := middleware.CurrentUser(c)
	if err != nil {
		c.Error(err)
		return
	}
	var p dto.PageParams
	if !bindQuery(c, &p) {
		return
	}
	p.Normalize()
	projects, total, err := h.projectSvc.ListMine(actor.ID, p.Page, p.PageSize)
	if err != nil {
		c.Error(err)
		return
	}
	util.OK(c, gin.H{"list": projects, "total": total, "page": p.Page, "page_size": p.PageSize})
}

// Update 更新项目基本信息。
func (h *ProjectHandler) Update(c *gin.Context) {
	actor, err := middleware.CurrentUser(c)
	if err != nil {
		c.Error(err)
		return
	}
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	var req dto.UpdateProjectRequest
	if !bindUpdateProject(c, &req) {
		return
	}
	project, err := h.projectSvc.Update(actor, id, &req)
	if err != nil {
		c.Error(err)
		return
	}
	h.auditSvc.Record(actor.ID, actor.Username, actor.Role, "project.update", "project", project.ID,
		"更新采访项目 "+project.Title, c.ClientIP(), middleware.RequestID(c))
	util.OKMessage(c, constants.MsgProjectUpdated, project)
}

// TransitionStatus 项目状态流转。
func (h *ProjectHandler) TransitionStatus(c *gin.Context) {
	actor, err := middleware.CurrentUser(c)
	if err != nil {
		c.Error(err)
		return
	}
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	var req dto.UpdateProjectStatusRequest
	if !bindJSON(c, &req) {
		return
	}
	project, err := h.projectSvc.TransitionStatus(actor, id, req.Status)
	if err != nil {
		c.Error(err)
		return
	}
	h.auditSvc.Record(actor.ID, actor.Username, actor.Role, "project.status", "project", project.ID,
		"项目状态流转为 "+req.Status, c.ClientIP(), middleware.RequestID(c))
	util.OKMessage(c, constants.MsgProjectStatusOK, project)
}

// Delete 删除项目。
func (h *ProjectHandler) Delete(c *gin.Context) {
	actor, err := middleware.CurrentUser(c)
	if err != nil {
		c.Error(err)
		return
	}
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	if err := h.projectSvc.Delete(actor, id); err != nil {
		c.Error(err)
		return
	}
	h.auditSvc.Record(actor.ID, actor.Username, actor.Role, "project.delete", "project", id,
		"删除采访项目", c.ClientIP(), middleware.RequestID(c))
	util.OKMessage(c, constants.MsgProjectDeleted, nil)
}

// Stats 项目统计。
func (h *ProjectHandler) Stats(c *gin.Context) {
	stats, err := h.projectSvc.Stats()
	if err != nil {
		c.Error(err)
		return
	}
	util.OK(c, stats)
}
