package service

import (
	"log/slog"
	"reflect"
	"testing"

	"github.com/oralhistory/oralhistory/internal/constants"
	"github.com/oralhistory/oralhistory/internal/dto"
	"github.com/oralhistory/oralhistory/internal/model"
	"github.com/oralhistory/oralhistory/internal/repository"
	"github.com/oralhistory/oralhistory/internal/util"
)

type fakeProjectRepo struct {
	projects       map[uint]*model.Project
	updated        *model.Project
	replacedTags   map[uint][]string
	updatedWithTag map[uint][]string
	lastFilter     repository.ProjectListFilter
	forUpdate      bool
	err            error
}

func newFakeProjectRepo() *fakeProjectRepo {
	return &fakeProjectRepo{
		projects:       map[uint]*model.Project{},
		replacedTags:   map[uint][]string{},
		updatedWithTag: map[uint][]string{},
	}
}

func (f *fakeProjectRepo) Create(project *model.Project) error {
	if f.err != nil {
		return f.err
	}
	if project.ID == 0 {
		project.ID = uint(len(f.projects) + 1)
	}
	copied := *project
	f.projects[project.ID] = &copied
	return nil
}
func (f *fakeProjectRepo) FindByID(id uint) (*model.Project, error) {
	if p, ok := f.projects[id]; ok {
		copied := *p
		return &copied, nil
	}
	return nil, repository.ErrNotFound
}
func (f *fakeProjectRepo) FindByIDForUpdate(id uint) (*model.Project, error) {
	f.forUpdate = true
	return f.FindByID(id)
}
func (f *fakeProjectRepo) List(page, pageSize int, filter repository.ProjectListFilter) ([]model.Project, int64, error) {
	f.lastFilter = filter
	return nil, 0, nil
}
func (f *fakeProjectRepo) ListByUser(userID uint, page, pageSize int) ([]model.Project, int64, error) {
	return nil, 0, nil
}
func (f *fakeProjectRepo) Update(project *model.Project) error {
	if f.err != nil {
		return f.err
	}
	f.updated = project
	f.projects[project.ID] = project
	return nil
}
func (f *fakeProjectRepo) UpdateStatus(project *model.Project) error {
	return f.Update(project)
}
func (f *fakeProjectRepo) UpdateWithTags(project *model.Project, names []string) error {
	if err := f.Update(project); err != nil {
		return err
	}
	f.updatedWithTag[project.ID] = append([]string{}, names...)
	project.TagNames = append([]string{}, names...)
	return nil
}
func (f *fakeProjectRepo) ReplaceTags(projectID uint, names []string) error {
	f.replacedTags[projectID] = append([]string{}, names...)
	return nil
}
func (f *fakeProjectRepo) ListTagsByProjectIDs(ids []uint) (map[uint][]string, error) {
	return map[uint][]string{}, nil
}
func (f *fakeProjectRepo) Delete(id uint) error  { return nil }
func (f *fakeProjectRepo) Count() (int64, error) { return 0, nil }

func TestProjectServiceTransitionStatus(t *testing.T) {
	actor := &model.User{ID: 1, Username: "interviewer", Role: constants.RoleInterviewer}
	cases := []struct {
		name    string
		from    string
		to      string
		wantErr bool
	}{
		{name: "draft to in_progress", from: constants.ProjectStatusDraft, to: constants.ProjectStatusInProgress},
		{name: "in_progress to completed", from: constants.ProjectStatusInProgress, to: constants.ProjectStatusCompleted},
		{name: "completed to archived", from: constants.ProjectStatusCompleted, to: constants.ProjectStatusArchived},
		{name: "draft to completed is invalid", from: constants.ProjectStatusDraft, to: constants.ProjectStatusCompleted, wantErr: true},
		{name: "archived cannot change", from: constants.ProjectStatusArchived, to: constants.ProjectStatusDraft, wantErr: true},
		{name: "unknown status rejected", from: constants.ProjectStatusDraft, to: "unknown", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakeProjectRepo()
			repo.projects[1] = &model.Project{ID: 1, Title: "测试项目", Status: tc.from}
			svc := NewProjectService(repo, slog.Default())
			got, err := svc.TransitionStatus(actor, 1, tc.to)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				var appErr *util.AppError
				if !asAppError(err, &appErr) {
					t.Fatalf("expected app error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Status != tc.to {
				t.Fatalf("status = %s, want %s", got.Status, tc.to)
			}
			if !repo.forUpdate {
				t.Fatalf("expected SELECT FOR UPDATE path used")
			}
		})
	}
}

func TestProjectServiceCreate(t *testing.T) {
	repo := newFakeProjectRepo()
	svc := NewProjectService(repo, slog.Default())
	actor := &model.User{ID: 2, Username: "archivist", Role: constants.RoleArchivist}
	req := &dto.CreateProjectRequest{
		Title:           "老城记忆",
		IntervieweeName: "王奶奶",
		BirthYear:       1938,
		Background:      "纺织厂退休工人",
	}
	project, err := svc.Create(actor, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if project.Status != constants.ProjectStatusDraft {
		t.Fatalf("default status = %s, want draft", project.Status)
	}
	if project.CreatedBy != actor.ID {
		t.Fatalf("created_by = %d, want %d", project.CreatedBy, actor.ID)
	}
	// 旧数据兼容：未提供标签时为空切片，而不是 nil。
	if project.TagNames == nil || len(project.TagNames) != 0 {
		t.Fatalf("tags = %v, want empty non-nil slice", project.TagNames)
	}
}

func TestProjectServiceCreateTagsDedupAndLimits(t *testing.T) {
	actor := &model.User{ID: 2, Username: "archivist", Role: constants.RoleArchivist}
	cases := []struct {
		name     string
		tags     []string
		want     []string
		wantErr  bool
		wantCode int
	}{
		{
			name: "trim and merge duplicates",
			tags: []string{"抗战", " 抗战 ", "知青", "知青", " 女工 "},
			want: []string{"抗战", "知青", "女工"},
		},
		{
			name: "exactly five accepted",
			tags: []string{"a", "b", "c", "d", "e"},
			want: []string{"a", "b", "c", "d", "e"},
		},
		{
			name:     "empty element rejected",
			tags:     []string{"抗战", "   "},
			wantErr:  true,
			wantCode: constants.CodeValidation,
		},
		{
			name:     "empty string element rejected",
			tags:     []string{""},
			wantErr:  true,
			wantCode: constants.CodeValidation,
		},
		{
			name:     "more than five rejected after dedup",
			tags:     []string{"a", "b", "c", "d", "e", "f"},
			wantErr:  true,
			wantCode: constants.CodeValidation,
		},
		{
			name: "six inputs collapsing to five accepted",
			tags: []string{"a", "b", "c", "d", "e", "e"},
			want: []string{"a", "b", "c", "d", "e"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakeProjectRepo()
			svc := NewProjectService(repo, slog.Default())
			req := &dto.CreateProjectRequest{
				Title: "项目", IntervieweeName: "受访者", BirthYear: 1940, Tags: tc.tags,
			}
			project, err := svc.Create(actor, req)
			if tc.wantErr {
				var appErr *util.AppError
				if !asAppError(err, &appErr) {
					t.Fatalf("expected app error, got %v", err)
				}
				if appErr.Code != tc.wantCode {
					t.Fatalf("code = %d, want %d", appErr.Code, tc.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(project.TagNames, tc.want) {
				t.Fatalf("tags = %v, want %v", project.TagNames, tc.want)
			}
		})
	}
}

func TestProjectServiceListCombinedFilter(t *testing.T) {
	repo := newFakeProjectRepo()
	svc := NewProjectService(repo, slog.Default())

	// 空条件：nil 查询与全空查询都应正常通过并落到空过滤条件。
	if _, _, err := svc.List(1, 20, nil); err != nil {
		t.Fatalf("nil query should be allowed: %v", err)
	}
	if repo.lastFilter.Status != "" || len(repo.lastFilter.Tags) != 0 || repo.lastFilter.Keyword != "" {
		t.Fatalf("empty filter = %+v, want zero value", repo.lastFilter)
	}
	if _, _, err := svc.List(1, 20, &dto.ProjectListQuery{}); err != nil {
		t.Fatalf("empty query should be allowed: %v", err)
	}

	// 组合条件：状态 + 重复标签（AND 前去重）+ 关键字（去空白）。
	query := &dto.ProjectListQuery{
		Status:  "in_progress",
		Tag:     []string{"抗战", " 抗战 ", "知青"},
		Keyword: "  王奶  ",
	}
	if _, _, err := svc.List(2, 10, query); err != nil {
		t.Fatalf("combined query failed: %v", err)
	}
	want := repository.ProjectListFilter{Status: "in_progress", Tags: []string{"抗战", "知青"}, Keyword: "王奶"}
	if !reflect.DeepEqual(repo.lastFilter, want) {
		t.Fatalf("filter = %+v, want %+v", repo.lastFilter, want)
	}

	// 非法状态仍要拒绝。
	if _, _, err := svc.List(1, 10, &dto.ProjectListQuery{Status: "bogus"}); err == nil {
		t.Fatalf("invalid status should be rejected")
	}
}

func TestProjectServiceUpdateTags(t *testing.T) {
	actor := &model.User{ID: 1, Username: "interviewer", Role: constants.RoleInterviewer}

	t.Run("archived project tags are readonly", func(t *testing.T) {
		repo := newFakeProjectRepo()
		repo.projects[7] = &model.Project{
			ID: 7, Title: "归档项目", Status: constants.ProjectStatusArchived, TagNames: []string{"老标签"},
		}
		svc := NewProjectService(repo, slog.Default())
		title := "新标题"
		req := &dto.UpdateProjectRequest{Title: &title, Tags: []string{"新标签"}, UpdateTags: true}
		_, err := svc.Update(actor, 7, req)
		var appErr *util.AppError
		if !asAppError(err, &appErr) {
			t.Fatalf("expected app error, got %v", err)
		}
		if appErr.Code != constants.CodeProjectStatus {
			t.Fatalf("code = %d, want %d", appErr.Code, constants.CodeProjectStatus)
		}
		if repo.updated != nil {
			t.Fatalf("archived project must not be saved")
		}
	})

	t.Run("duplicate tags merged on update", func(t *testing.T) {
		repo := newFakeProjectRepo()
		repo.projects[3] = &model.Project{ID: 3, Title: "进行中项目", Status: constants.ProjectStatusInProgress}
		svc := NewProjectService(repo, slog.Default())
		req := &dto.UpdateProjectRequest{Tags: []string{"a", "a", " b "}, UpdateTags: true}
		got, err := svc.Update(actor, 3, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		wantTags := []string{"a", "b"}
		if !reflect.DeepEqual(got.TagNames, wantTags) {
			t.Fatalf("tags = %v, want %v", got.TagNames, wantTags)
		}
		if !reflect.DeepEqual(repo.updatedWithTag[3], wantTags) {
			t.Fatalf("persisted tags = %v, want %v", repo.updatedWithTag[3], wantTags)
		}
	})

	t.Run("over limit rejected before persist", func(t *testing.T) {
		repo := newFakeProjectRepo()
		repo.projects[3] = &model.Project{ID: 3, Title: "进行中项目", Status: constants.ProjectStatusInProgress}
		svc := NewProjectService(repo, slog.Default())
		req := &dto.UpdateProjectRequest{Tags: []string{"a", "b", "c", "d", "e", "f"}, UpdateTags: true}
		if _, err := svc.Update(actor, 3, req); err == nil {
			t.Fatalf("expected error for >5 tags")
		}
		if repo.updated != nil || len(repo.updatedWithTag) > 0 {
			t.Fatalf("rejected request must not be persisted")
		}
	})

	t.Run("tags omitted stay untouched", func(t *testing.T) {
		repo := newFakeProjectRepo()
		repo.projects[3] = &model.Project{
			ID: 3, Title: "项目", Status: constants.ProjectStatusDraft, TagNames: []string{"保留"},
		}
		svc := NewProjectService(repo, slog.Default())
		req := &dto.UpdateProjectRequest{UpdateTags: false}
		got, err := svc.Update(actor, 3, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(repo.updatedWithTag) != 0 {
			t.Fatalf("tags must not be replaced when tags field omitted")
		}
		if !reflect.DeepEqual(got.TagNames, []string{"保留"}) {
			t.Fatalf("tags = %v, want unchanged", got.TagNames)
		}
	})

	t.Run("legacy project without tags accepts new tags", func(t *testing.T) {
		repo := newFakeProjectRepo()
		repo.projects[5] = &model.Project{ID: 5, Title: "旧项目", Status: constants.ProjectStatusCompleted}
		svc := NewProjectService(repo, slog.Default())
		req := &dto.UpdateProjectRequest{Tags: []string{"补录标签"}, UpdateTags: true}
		got, err := svc.Update(actor, 5, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(got.TagNames, []string{"补录标签"}) {
			t.Fatalf("tags = %v, want 补录标签", got.TagNames)
		}
	})
}

func asAppError(err error, target **util.AppError) bool {
	appErr, ok := err.(*util.AppError)
	if ok {
		*target = appErr
	}
	return ok
}
