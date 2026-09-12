package repository

import (
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/oralhistory/oralhistory/internal/model"
	"gorm.io/gorm"
)

func TestProjectRepositoryList(t *testing.T) {
	db, mock := newMockDB(t)
	repo := NewProjectRepository(db)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM `projects`")).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	rows := sqlmock.NewRows([]string{"id", "title", "interviewee_name", "status"}).
		AddRow(1, "老城记忆", "王奶奶", "draft").
		AddRow(2, "渡江战役亲历", "张爷爷", "in_progress")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `projects`")).
		WillReturnRows(rows)
	// 列表项标签批量加载，避免 N+1。
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `project_tags` WHERE project_id IN (?,?)")).
		WithArgs(uint(1), uint(2)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "project_id", "name"}).
			AddRow(10, 1, "抗战").
			AddRow(11, 1, "女工"))

	projects, total, err := repo.List(1, 10, ProjectListFilter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 2 || len(projects) != 2 {
		t.Fatalf("total=%d len=%d, want 2/2", total, len(projects))
	}
	if projects[1].Status != "in_progress" {
		t.Fatalf("status = %s, want in_progress", projects[1].Status)
	}
	if got := projects[0].TagNames; len(got) != 2 || got[0] != "抗战" || got[1] != "女工" {
		t.Fatalf("tags = %v, want [抗战 女工]", got)
	}
}

// TestProjectRepositoryListLegacyWithoutTags 旧数据：项目没有任何标签行时返回空切片而非 nil。
func TestProjectRepositoryListLegacyWithoutTags(t *testing.T) {
	db, mock := newMockDB(t)
	repo := NewProjectRepository(db)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM `projects`")).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `projects`")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "title", "interviewee_name", "status"}).
			AddRow(1, "旧项目", "老爷爷", "completed"))
	mock.ExpectQuery("SELECT \\* FROM `project_tags`").
		WillReturnRows(sqlmock.NewRows([]string{"id", "project_id", "name"}))

	projects, _, err := repo.List(1, 10, ProjectListFilter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if projects[0].TagNames == nil || len(projects[0].TagNames) != 0 {
		t.Fatalf("legacy tags = %v, want empty non-nil slice", projects[0].TagNames)
	}
}

// TestProjectRepositoryListCombinedFilterSQL 状态 + 双标签 AND + 关键字组合，并保持分页。
func TestProjectRepositoryListCombinedFilterSQL(t *testing.T) {
	db, mock := newMockDB(t)
	repo := NewProjectRepository(db)
	filter := ProjectListFilter{Status: "in_progress", Tags: []string{"抗战", "知青"}, Keyword: "王奶"}

	// count 查询：状态、标签 AND 子查询、标题/受访者 LIKE（显式 ESCAPE）三个条件同时出现。
	mock.ExpectQuery(`SELECT count\(\*\) FROM `+"`projects`"+
		` WHERE status = \? AND id IN \(SELECT `+"`project_id`"+` FROM `+"`project_tags`"+
		` WHERE name IN \(\?,\?\) GROUP BY `+"`project_id`"+
		` HAVING COUNT\(DISTINCT name\) >= \?\) AND \(title LIKE \? ESCAPE '\\' OR interviewee_name LIKE \? ESCAPE '\\'\)`).
		WithArgs("in_progress", "抗战", "知青", int64(2), "%王奶%", "%王奶%").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT \* FROM `+"`projects`").
		WithArgs("in_progress", "抗战", "知青", int64(2), "%王奶%", "%王奶%", 20).
		WillReturnRows(sqlmock.NewRows([]string{"id", "title", "interviewee_name", "status"}).
			AddRow(7, "王奶奶的知青岁月", "王奶奶", "in_progress"))
	mock.ExpectQuery("SELECT \\* FROM `project_tags`").
		WillReturnRows(sqlmock.NewRows([]string{"id", "project_id", "name"}).
			AddRow(1, 7, "抗战").AddRow(2, 7, "知青"))

	projects, total, err := repo.List(1, 20, filter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 1 || len(projects) != 1 {
		t.Fatalf("total=%d len=%d, want 1/1", total, len(projects))
	}
	if len(projects[0].TagNames) != 2 {
		t.Fatalf("tags = %v, want 2 matched tags", projects[0].TagNames)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestProjectRepositoryFindByID(t *testing.T) {
	db, mock := newMockDB(t)
	repo := NewProjectRepository(db)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `projects` WHERE `projects`.`id` = ?")).
		WithArgs(99, 1).
		WillReturnError(gorm.ErrRecordNotFound)

	if _, err := repo.FindByID(99); err == nil {
		t.Fatalf("expected not found error")
	} else if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestProjectRepositoryFindByIDWithTags(t *testing.T) {
	db, mock := newMockDB(t)
	mock.MatchExpectationsInOrder(false) // GORM Preload 执行顺序不保证
	repo := NewProjectRepository(db)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `projects` WHERE `projects`.`id` = ?")).
		WithArgs(3, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "title", "interviewee_name", "status"}).
			AddRow(3, "项目", "受访者", "draft"))
	mock.ExpectQuery("FROM `project_tags`").
		WillReturnRows(sqlmock.NewRows([]string{"id", "project_id", "name"}).
			AddRow(1, 3, "标签一").AddRow(2, 3, "标签二"))
	mock.ExpectQuery("FROM `questions`").WillReturnRows(sqlmock.NewRows(nil))
	mock.ExpectQuery("FROM `recordings`").WillReturnRows(sqlmock.NewRows(nil))
	mock.ExpectQuery("FROM `timeline_markers`").WillReturnRows(sqlmock.NewRows(nil))

	project, err := repo.FindByID(3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := project.TagNames; len(got) != 2 || got[0] != "标签一" || got[1] != "标签二" {
		t.Fatalf("tags = %v, want [标签一 标签二]", got)
	}
}

func TestProjectRepositoryCreateWithTags(t *testing.T) {
	db, mock := newMockDB(t)
	repo := NewProjectRepository(db)
	project := &model.Project{
		Title: "标签项目", IntervieweeName: "李爷爷", BirthYear: 1931,
		Status: "draft", CreatedBy: 1, TagNames: []string{"抗战", "老兵"},
	}

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO `projects`")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO `project_tags`")).
		WillReturnResult(sqlmock.NewResult(2, 2))
	mock.ExpectCommit()

	if err := repo.Create(project); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProjectRepositoryCreateWithoutTags(t *testing.T) {
	db, mock := newMockDB(t)
	repo := NewProjectRepository(db)
	project := &model.Project{Title: "无标签项目", IntervieweeName: "张奶奶", Status: "draft", CreatedBy: 1}

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO `projects`")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	if err := repo.Create(project); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProjectRepositoryUpdateWithTags(t *testing.T) {
	db, mock := newMockDB(t)
	repo := NewProjectRepository(db)
	project := &model.Project{ID: 1, Title: "改名", IntervieweeName: "受访者", Status: "draft", CreatedBy: 1}

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `projects` SET")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM `project_tags` WHERE project_id = ?")).
		WithArgs(1).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO `project_tags`")).
		WillReturnResult(sqlmock.NewResult(3, 1))
	mock.ExpectCommit()

	if err := repo.UpdateWithTags(project, []string{"新标签"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProjectRepositoryUpdateWithClearTags(t *testing.T) {
	db, mock := newMockDB(t)
	repo := NewProjectRepository(db)
	project := &model.Project{ID: 1, Title: "项目", IntervieweeName: "受访者", Status: "draft", CreatedBy: 1}

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `projects` SET")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM `project_tags` WHERE project_id = ?")).
		WithArgs(1).
		WillReturnResult(sqlmock.NewResult(0, 3))
	// 清空标签时不应再执行 INSERT。
	mock.ExpectCommit()

	if err := repo.UpdateWithTags(project, []string{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestProjectRepositoryUpdateStatus(t *testing.T) {
	db, mock := newMockDB(t)
	repo := NewProjectRepository(db)
	project := &model.Project{ID: 1, Status: "completed"}

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `projects` SET `status`=?,`updated_at`=? WHERE `id` = ?")).
		WithArgs("completed", sqlmock.AnyArg(), 1).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	if err := repo.UpdateStatus(project); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEscapeLike(t *testing.T) {
	cases := map[string]string{
		"王奶":   "王奶",
		"100%": `100\%`,
		"a_b":  `a\_b`,
		`c\d`:  `c\\d`,
	}
	for in, want := range cases {
		if got := escapeLike(in); got != want {
			t.Fatalf("escapeLike(%q) = %q, want %q", in, got, want)
		}
	}
}
