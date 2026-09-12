package dto

import "strings"

// CreateProjectRequest 创建采访项目请求。
type CreateProjectRequest struct {
	Title           string   `json:"title" binding:"required,min=1,max=128"`
	IntervieweeName string   `json:"interviewee_name" binding:"required,min=1,max=64"`
	BirthYear       int      `json:"birth_year" binding:"required,min=1900,max=2100"`
	Background      string   `json:"background" binding:"omitempty,max=2000"`
	Status          string   `json:"status" binding:"omitempty,oneof=draft in_progress completed archived"`
	Tags            []string `json:"tags"`
}

// UpdateProjectRequest 更新采访项目请求。Tags 为 nil（字段缺省）表示不修改标签；
// 为空数组表示清空标签；元素去空白后不允许为空。
type UpdateProjectRequest struct {
	Title           *string  `json:"title" binding:"omitempty,min=1,max=128"`
	IntervieweeName *string  `json:"interviewee_name" binding:"omitempty,min=1,max=64"`
	BirthYear       *int     `json:"birth_year" binding:"omitempty,min=1900,max=2100"`
	Background      *string  `json:"background" binding:"omitempty,max=2000"`
	Tags            []string `json:"tags"`
	UpdateTags      bool     `json:"-"`
}

// UpdateProjectStatusRequest 项目状态流转请求。
type UpdateProjectStatusRequest struct {
	Status string `json:"status" binding:"required,oneof=draft in_progress completed archived"`
}

// ProjectListQuery 项目列表检索条件：状态、标签（多个为 AND）、标题/受访者关键字可组合。
type ProjectListQuery struct {
	Page     int      `form:"page" json:"page"`
	PageSize int      `form:"page_size" json:"page_size"`
	Status   string   `form:"status" json:"status"`
	Tag      []string `form:"tag" json:"tag"`
	Keyword  string   `form:"keyword" json:"keyword"`
}

// Normalize 归一化分页并清理条件两端空白。
func (q *ProjectListQuery) Normalize() {
	if q.Page <= 0 {
		q.Page = 1
	}
	if q.PageSize <= 0 || q.PageSize > 100 {
		q.PageSize = 20
	}
	q.Status = strings.TrimSpace(q.Status)
	q.Keyword = strings.TrimSpace(q.Keyword)
	cleaned := make([]string, 0, len(q.Tag))
	for _, t := range q.Tag {
		if name := strings.TrimSpace(t); name != "" {
			cleaned = append(cleaned, name)
		}
	}
	q.Tag = cleaned
}

// ProjectResponse 项目响应。
type ProjectResponse struct {
	ID              uint     `json:"id"`
	Title           string   `json:"title"`
	IntervieweeName string   `json:"interviewee_name"`
	BirthYear       int      `json:"birth_year"`
	Background      string   `json:"background"`
	Status          string   `json:"status"`
	Tags            []string `json:"tags"`
	CreatedBy       uint     `json:"created_by"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
}
