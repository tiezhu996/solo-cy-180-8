package service

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/oralhistory/oralhistory/internal/constants"
	"github.com/oralhistory/oralhistory/internal/dto"
	"github.com/oralhistory/oralhistory/internal/model"
	"github.com/oralhistory/oralhistory/internal/repository"
	"github.com/oralhistory/oralhistory/internal/util"
)

// ProjectService 采访项目业务接口。
type ProjectService interface {
	Create(actor *model.User, req *dto.CreateProjectRequest) (*model.Project, error)
	Get(id uint) (*model.Project, error)
	List(page, pageSize int, query *dto.ProjectListQuery) ([]model.Project, int64, error)
	ListMine(actorID uint, page, pageSize int) ([]model.Project, int64, error)
	Update(actor *model.User, id uint, req *dto.UpdateProjectRequest) (*model.Project, error)
	TransitionStatus(actor *model.User, id uint, status string) (*model.Project, error)
	Delete(actor *model.User, id uint) error
	Stats() (map[string]any, error)
}

type projectService struct {
	projectRepo repository.ProjectRepository
	logger      *slog.Logger
}

// NewProjectService 构造项目服务。
func NewProjectService(projectRepo repository.ProjectRepository, logger *slog.Logger) ProjectService {
	return &projectService{projectRepo: projectRepo, logger: logger}
}

// normalizeTags 规整标签入参：逐项去首尾空白，空元素直接拒绝；
// 合法项按首次出现顺序去重合并。结果超过 5 个时拒绝。
func normalizeTags(raw []string) ([]string, error) {
	seen := make(map[string]struct{}, len(raw))
	tags := make([]string, 0, len(raw))
	for _, item := range raw {
		name := strings.TrimSpace(item)
		if name == "" {
			return nil, util.NewAppError(constants.CodeValidation,
				fmt.Sprintf("项目标签不能为空，非法输入：%q", item), nil)
		}
		if len([]rune(name)) > constants.ProjectTagMaxLength {
			return nil, util.NewAppError(constants.CodeValidation,
				fmt.Sprintf("项目标签 %s 超过 %d 个字符上限", name, constants.ProjectTagMaxLength), nil)
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		tags = append(tags, name)
	}
	if len(tags) > constants.ProjectTagMaxCount {
		return nil, util.NewAppError(constants.CodeValidation,
			fmt.Sprintf("项目标签最多 %d 个，当前去重后为 %d 个", constants.ProjectTagMaxCount, len(tags)), nil)
	}
	return tags, nil
}

func (s *projectService) Create(actor *model.User, req *dto.CreateProjectRequest) (*model.Project, error) {
	status := req.Status
	if status == "" {
		status = constants.ProjectStatusDraft
	}
	if !constants.ValidProjectStatus(status) {
		return nil, util.NewAppError(constants.CodeValidation, fmt.Sprintf("项目状态 %s 不合法", status), nil)
	}
	tags, err := normalizeTags(req.Tags)
	if err != nil {
		return nil, err
	}
	project := &model.Project{
		Title:           req.Title,
		IntervieweeName: req.IntervieweeName,
		BirthYear:       req.BirthYear,
		Background:      req.Background,
		Status:          status,
		CreatedBy:       actor.ID,
		TagNames:        tags,
	}
	if err := s.projectRepo.Create(project); err != nil {
		return nil, util.NewAppError(constants.CodeInternal, fmt.Sprintf("创建项目 %s 失败", req.Title), err)
	}
	s.logger.Info(fmt.Sprintf(constants.LogProjectCreate, actor.Username, project.Title, project.IntervieweeName, project.Status, len(tags)))
	return project, nil
}

func (s *projectService) Get(id uint) (*model.Project, error) {
	project, err := s.projectRepo.FindByID(id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, util.NewAppError(constants.CodeNotFound, fmt.Sprintf("项目 %d 不存在", id), err)
		}
		return nil, util.NewAppError(constants.CodeInternal, fmt.Sprintf("查询项目 %d 失败", id), err)
	}
	return project, nil
}

func (s *projectService) List(page, pageSize int, query *dto.ProjectListQuery) ([]model.Project, int64, error) {
	if query == nil {
		query = &dto.ProjectListQuery{}
	}
	if query.Status != "" && !constants.ValidProjectStatus(query.Status) {
		return nil, 0, util.NewAppError(constants.CodeValidation, fmt.Sprintf("项目状态 %s 不合法", query.Status), nil)
	}
	tags, err := normalizeTags(query.Tag)
	if err != nil {
		return nil, 0, err
	}
	projects, total, err := s.projectRepo.List(page, pageSize, repository.ProjectListFilter{
		Status:  query.Status,
		Tags:    tags,
		Keyword: strings.TrimSpace(query.Keyword),
	})
	if err != nil {
		return nil, 0, util.NewAppError(constants.CodeInternal, "项目列表查询失败", err)
	}
	return projects, total, nil
}

func (s *projectService) ListMine(actorID uint, page, pageSize int) ([]model.Project, int64, error) {
	projects, total, err := s.projectRepo.ListByUser(actorID, page, pageSize)
	if err != nil {
		return nil, 0, util.NewAppError(constants.CodeInternal, fmt.Sprintf("查询用户 %d 项目列表失败", actorID), err)
	}
	return projects, total, nil
}

func (s *projectService) Update(actor *model.User, id uint, req *dto.UpdateProjectRequest) (*model.Project, error) {
	project, err := s.projectRepo.FindByID(id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, util.NewAppError(constants.CodeNotFound, fmt.Sprintf("项目 %d 不存在", id), err)
		}
		return nil, util.NewAppError(constants.CodeInternal, fmt.Sprintf("查询项目 %d 失败", id), err)
	}
	// 归档项目只读：基本信息与标签均不允许修改。
	if project.Status == constants.ProjectStatusArchived {
		return nil, util.NewAppError(constants.CodeProjectStatus,
			fmt.Sprintf("项目 %d 已归档（状态 %s），标签与基本信息均为只读，禁止修改", id, project.Status), nil)
	}
	if req.Title != nil {
		project.Title = *req.Title
	}
	if req.IntervieweeName != nil {
		project.IntervieweeName = *req.IntervieweeName
	}
	if req.BirthYear != nil {
		project.BirthYear = *req.BirthYear
	}
	if req.Background != nil {
		project.Background = *req.Background
	}

	var nextTags []string
	if req.UpdateTags {
		tags, err := normalizeTags(req.Tags)
		if err != nil {
			return nil, err
		}
		nextTags = tags
	}

	if req.UpdateTags {
		tags, err := normalizeTags(req.Tags)
		if err != nil {
			return nil, err
		}
		nextTags = tags
	}

	if req.UpdateTags {
		if err := s.projectRepo.UpdateWithTags(project, nextTags); err != nil {
			return nil, util.NewAppError(constants.CodeInternal, fmt.Sprintf("更新项目 %d 及其标签失败", id), err)
		}
		project.TagNames = nextTags
		s.logger.Info(fmt.Sprintf(constants.LogProjectTagsUpdate, actor.Username, project.ID, len(nextTags)))
	} else if err := s.projectRepo.Update(project); err != nil {
		return nil, util.NewAppError(constants.CodeInternal, fmt.Sprintf("更新项目 %d 失败", id), err)
	}
	s.logger.Info(fmt.Sprintf(constants.LogProjectUpdate, actor.Username, project.ID, project.Title, project.Status))
	return project, nil
}

func (s *projectService) TransitionStatus(actor *model.User, id uint, status string) (*model.Project, error) {
	if !constants.ValidProjectStatus(status) {
		return nil, util.NewAppError(constants.CodeValidation, fmt.Sprintf("项目状态 %s 不合法", status), nil)
	}
	// 并发场景使用 SELECT ... FOR UPDATE 锁定行。
	project, err := s.projectRepo.FindByIDForUpdate(id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, util.NewAppError(constants.CodeNotFound, fmt.Sprintf("项目 %d 不存在", id), err)
		}
		return nil, util.NewAppError(constants.CodeInternal, fmt.Sprintf("查询项目 %d 失败", id), err)
	}
	if !constants.CanTransitionProject(project.Status, status) {
		return nil, util.NewAppError(constants.CodeProjectStatus,
			fmt.Sprintf("项目 %d 状态不允许从 %s 流转到 %s", id, project.Status, status), nil)
	}
	from := project.Status
	project.Status = status
	if err := s.projectRepo.UpdateStatus(project); err != nil {
		return nil, util.NewAppError(constants.CodeInternal, fmt.Sprintf("项目 %d 状态更新失败", id), err)
	}
	s.logger.Info(fmt.Sprintf(constants.LogProjectStatus, actor.Username, project.ID, from, status))
	// 重新加载详情，保证响应中包含标签等完整字段。
	project, err = s.projectRepo.FindByID(id)
	if err != nil {
		return nil, util.NewAppError(constants.CodeInternal, fmt.Sprintf("查询项目 %d 失败", id), err)
	}
	return project, nil
}

func (s *projectService) Delete(actor *model.User, id uint) error {
	if _, err := s.projectRepo.FindByID(id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return util.NewAppError(constants.CodeNotFound, fmt.Sprintf("项目 %d 不存在", id), err)
		}
		return util.NewAppError(constants.CodeInternal, fmt.Sprintf("查询项目 %d 失败", id), err)
	}
	if err := s.projectRepo.Delete(id); err != nil {
		return util.NewAppError(constants.CodeInternal, fmt.Sprintf("删除项目 %d 失败", id), err)
	}
	s.logger.Info(fmt.Sprintf(constants.LogProjectDelete, actor.Username, id))
	return nil
}

func (s *projectService) Stats() (map[string]any, error) {
	total, err := s.projectRepo.Count()
	if err != nil {
		return nil, util.NewAppError(constants.CodeInternal, "项目统计失败", err)
	}
	return map[string]any{"project_total": total}, nil
}
