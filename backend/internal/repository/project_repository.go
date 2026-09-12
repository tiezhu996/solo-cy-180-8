package repository

import (
	"errors"
	"fmt"
	"strings"

	"github.com/oralhistory/oralhistory/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ProjectListFilter 项目列表检索条件，各条件之间为 AND 组合：
// Status 按状态精确筛选；Tags 多标签时要求项目同时命中（AND）；
// Keyword 在标题与受访者姓名上做模糊匹配。
type ProjectListFilter struct {
	Status  string
	Tags    []string
	Keyword string
}

// ProjectRepository 采访项目数据访问接口。
type ProjectRepository interface {
	Create(project *model.Project) error
	FindByID(id uint) (*model.Project, error)
	List(page, pageSize int, filter ProjectListFilter) ([]model.Project, int64, error)
	ListByUser(userID uint, page, pageSize int) ([]model.Project, int64, error)
	FindByIDForUpdate(id uint) (*model.Project, error)
	Update(project *model.Project) error
	UpdateStatus(project *model.Project) error
	UpdateWithTags(project *model.Project, names []string) error
	ReplaceTags(projectID uint, names []string) error
	ListTagsByProjectIDs(ids []uint) (map[uint][]string, error)
	Delete(id uint) error
	Count() (int64, error)
}

type projectRepository struct {
	db *gorm.DB
}

// NewProjectRepository 构造项目仓储。
func NewProjectRepository(db *gorm.DB) ProjectRepository {
	return &projectRepository{db: db}
}

func (r *projectRepository) Create(project *model.Project) error {
	// 项目主表与标签写入放入同一事务。
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit(clause.Associations).Create(project).Error; err != nil {
			return fmt.Errorf("create project %s: %w", project.Title, err)
		}
		if len(project.TagNames) > 0 {
			if err := insertProjectTags(tx, project.ID, project.TagNames); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

func (r *projectRepository) FindByID(id uint) (*model.Project, error) {
	var project model.Project
	if err := r.db.
		Preload("Tags", func(db *gorm.DB) *gorm.DB { return db.Order("project_tags.id ASC") }).
		Preload("Questions").Preload("Recordings").Preload("TimelineMarkers").
		First(&project, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("find project by id %d: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("find project by id: %w", err)
	}
	project.TagNames = tagNamesOf(project.Tags)
	return &project, nil
}

func (r *projectRepository) List(page, pageSize int, filter ProjectListFilter) ([]model.Project, int64, error) {
	var projects []model.Project
	var total int64
	q := r.applyFilter(r.db.Model(&model.Project{}), filter)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count projects: %w", err)
	}
	if err := q.Scopes(paginate(page, pageSize)).Order("id DESC").Find(&projects).Error; err != nil {
		return nil, 0, fmt.Errorf("list projects: %w", err)
	}
	if err := r.attachTags(projects); err != nil {
		return nil, 0, err
	}
	return projects, total, nil
}

func (r *projectRepository) ListByUser(userID uint, page, pageSize int) ([]model.Project, int64, error) {
	var projects []model.Project
	var total int64
	base := r.db.Model(&model.Project{}).Where("created_by = ?", userID)
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count projects by user %d: %w", userID, err)
	}
	if err := r.db.Model(&model.Project{}).Where("created_by = ?", userID).Scopes(paginate(page, pageSize)).
		Order("id DESC").Find(&projects).Error; err != nil {
		return nil, 0, fmt.Errorf("list projects by user: %w", err)
	}
	if err := r.attachTags(projects); err != nil {
		return nil, 0, err
	}
	return projects, total, nil
}

func (r *projectRepository) FindByIDForUpdate(id uint) (*model.Project, error) {
	var project model.Project
	if err := r.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&project, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("find project for update by id %d: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("find project for update: %w", err)
	}
	return &project, nil
}

func (r *projectRepository) Update(project *model.Project) error {
	// 标签由 ReplaceTags 单独维护，保存主表时显式跳过关联。
	if err := r.db.Omit(clause.Associations).Save(project).Error; err != nil {
		return fmt.Errorf("update project %d: %w", project.ID, err)
	}
	return nil
}

func (r *projectRepository) UpdateStatus(project *model.Project) error {
	if err := r.db.Model(project).Update("status", project.Status).Error; err != nil {
		return fmt.Errorf("update project %d status: %w", project.ID, err)
	}
	return nil
}

// UpdateWithTags 在同一事务内更新项目主表并整体替换标签（多步写）。
func (r *projectRepository) UpdateWithTags(project *model.Project, names []string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit(clause.Associations).Save(project).Error; err != nil {
			return fmt.Errorf("update project %d: %w", project.ID, err)
		}
		if err := tx.Where("project_id = ?", project.ID).Delete(&model.ProjectTag{}).Error; err != nil {
			return fmt.Errorf("delete tags of project %d: %w", project.ID, err)
		}
		if len(names) > 0 {
			if err := insertProjectTags(tx, project.ID, names); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *projectRepository) ReplaceTags(projectID uint, names []string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("project_id = ?", projectID).Delete(&model.ProjectTag{}).Error; err != nil {
			return fmt.Errorf("delete tags of project %d: %w", projectID, err)
		}
		if len(names) > 0 {
			if err := insertProjectTags(tx, projectID, names); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *projectRepository) ListTagsByProjectIDs(ids []uint) (map[uint][]string, error) {
	result := make(map[uint][]string, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	var tags []model.ProjectTag
	if err := r.db.Where("project_id IN ?", ids).Order("id ASC").Find(&tags).Error; err != nil {
		return nil, fmt.Errorf("list tags by project ids: %w", err)
	}
	for _, t := range tags {
		result[t.ProjectID] = append(result[t.ProjectID], t.Name)
	}
	return result, nil
}

func (r *projectRepository) Delete(id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("project_id = ?", id).Delete(&model.ProjectTag{}).Error; err != nil {
			return fmt.Errorf("delete tags of project %d: %w", id, err)
		}
		if err := tx.Where("project_id = ?", id).Delete(&model.TimelineMarker{}).Error; err != nil {
			return fmt.Errorf("delete markers of project %d: %w", id, err)
		}
		if err := tx.Where("project_id = ?", id).Delete(&model.Recording{}).Error; err != nil {
			return fmt.Errorf("delete recordings of project %d: %w", id, err)
		}
		if err := tx.Where("project_id = ?", id).Delete(&model.Question{}).Error; err != nil {
			return fmt.Errorf("delete questions of project %d: %w", id, err)
		}
		if err := tx.Delete(&model.Project{}, id).Error; err != nil {
			return fmt.Errorf("delete project %d: %w", id, err)
		}
		return nil
	})
}

func (r *projectRepository) Count() (int64, error) {
	var total int64
	if err := r.db.Model(&model.Project{}).Count(&total).Error; err != nil {
		return 0, fmt.Errorf("count projects: %w", err)
	}
	return total, nil
}

// applyFilter 叠加状态/标签/关键字组合条件（AND）。
func (r *projectRepository) applyFilter(q *gorm.DB, filter ProjectListFilter) *gorm.DB {
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	if len(filter.Tags) > 0 {
		tagSubQuery := r.db.Model(&model.ProjectTag{}).
			Select("project_id").
			Where("name IN ?", filter.Tags).
			Group("project_id").
			Having("COUNT(DISTINCT name) >= ?", len(filter.Tags))
		q = q.Where("id IN (?)", tagSubQuery)
	}
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		like := "%" + escapeLike(keyword) + "%"
		q = q.Where("title LIKE ? OR interviewee_name LIKE ?", like, like)
	}
	return q
}

// attachTags 批量填充列表项的 TagNames，避免 N+1 查询；旧项目无标签时为空切片。
func (r *projectRepository) attachTags(projects []model.Project) error {
	if len(projects) == 0 {
		return nil
	}
	ids := make([]uint, 0, len(projects))
	for _, p := range projects {
		ids = append(ids, p.ID)
	}
	tagsMap, err := r.ListTagsByProjectIDs(ids)
	if err != nil {
		return err
	}
	for i := range projects {
		names := tagsMap[projects[i].ID]
		if names == nil {
			names = []string{}
		}
		projects[i].TagNames = names
	}
	return nil
}

func insertProjectTags(tx *gorm.DB, projectID uint, names []string) error {
	tags := make([]model.ProjectTag, 0, len(names))
	for _, name := range names {
		tags = append(tags, model.ProjectTag{ProjectID: projectID, Name: name})
	}
	if err := tx.Create(&tags).Error; err != nil {
		return fmt.Errorf("create tags of project %d: %w", projectID, err)
	}
	return nil
}

func tagNamesOf(tags []model.ProjectTag) []string {
	names := make([]string, 0, len(tags))
	for _, t := range tags {
		names = append(names, t.Name)
	}
	return names
}

// escapeLike 转义 LIKE 通配符，避免关键字中的 %、_、\ 被当成模式。
func escapeLike(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(s)
}
