package repository

import (
	"sort"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/oralhistory/oralhistory/internal/model"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// newRegressionDB 构造可重复运行的真实文件/内存 SQLite 库（WAL + busy_timeout 支持并发连接），
// 直接使用生产迁移清单建表，确保被测查询与线上 MySQL 行为一致。
func newRegressionDB(t *testing.T, dsn string) (*gorm.DB, ProjectRepository) {
	t.Helper()
	if dsn == "" {
		dsn = "file:" + t.Name() + "?mode=memory&cache=shared&_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)"
	}
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(4)
	if err := db.AutoMigrate(
		&model.User{}, &model.Project{}, &model.ProjectTag{},
		&model.Question{}, &model.Recording{}, &model.TimelineMarker{}, &model.AuditLog{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db, NewProjectRepository(db)
}

func seedProject(t *testing.T, repo ProjectRepository, title, who, status string, tags []string) uint {
	t.Helper()
	p := &model.Project{
		Title: title, IntervieweeName: who, BirthYear: 1935,
		Status: status, CreatedBy: 1, TagNames: tags,
	}
	if err := repo.Create(p); err != nil {
		t.Fatalf("seed project %s: %v", title, err)
	}
	return p.ID
}

// expectSet 忽略列表的 id DESC 排序，只判定命中的项目集合是否符合业务预期。
func expectSet(t *testing.T, projects []model.Project, total int64, want []string, name string) {
	t.Helper()
	if int(total) != len(want) {
		t.Fatalf("business rule broken: total = %d, want %d (case=%s)", total, len(want), name)
	}
	if len(projects) != len(want) {
		gotNames := make([]string, 0, len(projects))
		for _, p := range projects {
			gotNames = append(gotNames, p.Title)
		}
		t.Fatalf("business rule broken: returned %d rows, want %d (case=%s, got=%v)", len(projects), len(want), name, gotNames)
	}
	wantSet := map[string]bool{}
	for _, w := range want {
		wantSet[w] = true
	}
	for _, p := range projects {
		if !wantSet[p.Title] {
			t.Fatalf("business rule broken: unexpected project %q in result, want %v (case=%s)", p.Title, want, name)
		}
	}
}

// TestRegressionCombinedFilterMatrix 多标签 AND + 关键字 + 状态组合筛选矩阵。
// 每条断言独立描述一条业务规则，失败信息即被破坏的行为。
func TestRegressionCombinedFilterMatrix(t *testing.T) {
	_, repo := newRegressionDB(t, "")
	// 数据集（id 自增，列表默认按 id DESC 返回）：
	// P1 王奶奶的知青岁月  in_progress  [抗战, 知青]
	// P2 王爷爷抗战纪实    in_progress  [抗战, 老兵]
	// P3 纺织厂女工口述    completed    [知青, 女工]
	// P4 老城故事          draft        []
	// P5 100%老兵回忆录    completed    [老兵]
	seedProject(t, repo, "王奶奶的知青岁月", "王奶奶", "in_progress", []string{"抗战", "知青"})
	seedProject(t, repo, "王爷爷抗战纪实", "王爷爷", "in_progress", []string{"抗战", "老兵"})
	seedProject(t, repo, "纺织厂女工口述", "李奶奶", "completed", []string{"知青", "女工"})
	seedProject(t, repo, "老城故事", "赵叔叔", "draft", nil)
	seedProject(t, repo, "100%老兵回忆录", "王老兵", "completed", []string{"老兵"})

	cases := []struct {
		name   string
		filter ProjectListFilter
		want   []string
	}{
		{
			name:   "empty filter returns every project",
			filter: ProjectListFilter{},
			want:   []string{"100%老兵回忆录", "纺织厂女工口述", "王奶奶的知青岁月", "王爷爷抗战纪实", "老城故事"},
		},
		{
			name:   "status only",
			filter: ProjectListFilter{Status: "in_progress"},
			want:   []string{"王奶奶的知青岁月", "王爷爷抗战纪实"},
		},
		{
			name:   "single tag hits multiple projects",
			filter: ProjectListFilter{Tags: []string{"抗战"}},
			want:   []string{"王奶奶的知青岁月", "王爷爷抗战纪实"},
		},
		{
			name:   "multiple tags are AND not OR",
			filter: ProjectListFilter{Tags: []string{"抗战", "知青"}},
			want:   []string{"王奶奶的知青岁月"}, // 只有 P1 同时拥有两个标签，P2 仅命中“抗战”
		},
		{
			name:   "tag plus status combined",
			filter: ProjectListFilter{Status: "completed", Tags: []string{"知青"}},
			want:   []string{"纺织厂女工口述"},
		},
		{
			name:   "keyword matches title",
			filter: ProjectListFilter{Keyword: "纺织厂"},
			want:   []string{"纺织厂女工口述"},
		},
		{
			name:   "keyword matches interviewee name",
			filter: ProjectListFilter{Keyword: "赵叔叔"},
			want:   []string{"老城故事"},
		},
		{
			name:   "status plus multi-tag AND plus keyword all combined",
			filter: ProjectListFilter{Status: "in_progress", Tags: []string{"抗战", "知青"}, Keyword: "王奶"},
			want:   []string{"王奶奶的知青岁月"},
		},
		{
			name:   "keyword percent is literal not wildcard",
			filter: ProjectListFilter{Keyword: "100%"},
			want:   []string{"100%老兵回忆录"},
		},
		{
			name:   "nonexistent status yields no rows",
			filter: ProjectListFilter{Status: "nonexistent_status_zzz"},
			want:   nil,
		},
		{
			name:   "no match yields empty non-error result",
			filter: ProjectListFilter{Tags: []string{"不存在的标签"}},
			want:   nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, total, err := repo.List(1, 50, tc.filter)
			if err != nil {
				t.Fatalf("business rule broken: combined filter query failed: %v", err)
			}
			expectSet(t, got, total, tc.want, tc.name)
		})
	}
}

// TestRegressionKeywordUnderscoreLiteral 下划线必须被当字面量而非 LIKE 单字符通配。
func TestRegressionKeywordUnderscoreLiteral(t *testing.T) {
	_, repo := newRegressionDB(t, "")
	seedProject(t, repo, "a_b项目", "受访者甲", "draft", nil)
	seedProject(t, repo, "axb项目", "受访者乙", "draft", nil)

	got, total, err := repo.List(1, 10, ProjectListFilter{Keyword: "a_b"})
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if total != 1 {
		t.Fatalf("business rule broken: underscore must match literally, got %d rows", total)
	}
	if got[0].Title != "a_b项目" {
		t.Fatalf("business rule broken: expected literal a_b match, got %q", got[0].Title)
	}
}

// TestRegressionConcurrentDuplicateTagWrites 并发写入同一标签：
// 唯一索引必须保证最终只有一行，其余请求收到约束冲突错误，绝不允许重复标签落库。
func TestRegressionConcurrentDuplicateTagWrites(t *testing.T) {
	db, repo := newRegressionDB(t, "")
	projectID := seedProject(t, repo, "并发项目", "受访者", "draft", nil)

	const writers = 16
	var wg sync.WaitGroup
	var success, rejected int64
	var mu sync.Mutex
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func() {
			defer wg.Done()
			tag := &model.ProjectTag{ProjectID: projectID, Name: "热点标签"}
			err := db.Create(tag).Error
			mu.Lock()
			if err == nil {
				success++
			} else {
				rejected++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	if success != 1 {
		t.Fatalf("business rule broken: duplicate tag writers success=%d, want exactly 1", success)
	}
	if rejected != writers-1 {
		t.Fatalf("business rule broken: duplicate tag writers rejected=%d, want %d", rejected, writers-1)
	}
	var count int64
	if err := db.Model(&model.ProjectTag{}).
		Where("project_id = ? AND name = ?", projectID, "热点标签").Count(&count).Error; err != nil {
		t.Fatalf("count tags: %v", err)
	}
	if count != 1 {
		t.Fatalf("business rule broken: UNIQUE(project_id,name) violated, persisted rows = %d, want 1", count)
	}
}

// TestRegressionConcurrentReplaceTags 并发的“整体替换标签”事务：
// 每个事务都是先删后插，最终状态必须是某一次完整替换的结果——标签数正确且无重复、无半截状态。
func TestRegressionConcurrentReplaceTags(t *testing.T) {
	db, repo := newRegressionDB(t, "")
	projectID := seedProject(t, repo, "并发替换项目", "受访者", "draft", []string{"初始标签"})

	sets := [][]string{
		{"抗战", "知青"},
		{"女工", "纺织厂", "退休"},
		{"抗战"},
	}
	const rounds = 8
	var wg sync.WaitGroup
	var failures int64
	var mu sync.Mutex
	wg.Add(rounds)
	for i := 0; i < rounds; i++ {
		i := i
		go func() {
			defer wg.Done()
			if err := repo.ReplaceTags(projectID, sets[i%len(sets)]); err != nil {
				mu.Lock()
				failures++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// SQLite 在高并发下允许 busy 冲突失败（MySQL 上由行锁串行化），但已成功的事务必须完整。
	var rows []model.ProjectTag
	if err := db.Where("project_id = ?", projectID).Order("id asc").Find(&rows).Error; err != nil {
		t.Fatalf("load final tags: %v", err)
	}
	names := make(map[string]int, len(rows))
	for _, r := range rows {
		names[r.Name]++
	}
	for name, n := range names {
		if n != 1 {
			t.Fatalf("business rule broken: concurrent replace left duplicate tag %q x%d", name, n)
		}
	}
	final := make([]string, 0, len(names))
	for name := range names {
		final = append(final, name)
	}
	sort.Strings(final)
	matchedOneSet := false
	for _, s := range sets {
		sorted := append([]string{}, s...)
		sort.Strings(sorted)
		if eqStrings(final, sorted) {
			matchedOneSet = true
			break
		}
	}
	if !matchedOneSet {
		t.Fatalf("business rule broken: final tags %v is not a complete result of any replace set (failures=%d)", final, failures)
	}

	// 仓储读出的标签与表内一致。
	tagsMap, err := repo.ListTagsByProjectIDs([]uint{projectID})
	if err != nil {
		t.Fatalf("list tags: %v", err)
	}
	if len(tagsMap[projectID]) != len(final) {
		t.Fatalf("business rule broken: repo view %v inconsistent with table state %v", tagsMap[projectID], final)
	}
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRegressionLegacyProjectsEmptyTags 旧项目（无任何标签行）列表与批量标签接口都返回空集合，不报错。
func TestRegressionLegacyProjectsEmptyTags(t *testing.T) {
	db, repo := newRegressionDB(t, "")
	// 直接写主表、不写标签，模拟旧数据。
	legacy := &model.Project{Title: "历史项目", IntervieweeName: "老人", BirthYear: 1925, Status: "completed", CreatedBy: 1}
	if err := db.Omit("Tags").Create(legacy).Error; err != nil {
		t.Fatalf("seed legacy project: %v", err)
	}

	got, total, err := repo.List(1, 10, ProjectListFilter{})
	if err != nil {
		t.Fatalf("business rule broken: listing legacy projects failed: %v", err)
	}
	if total != 1 || len(got) != 1 {
		t.Fatalf("business rule broken: legacy project missing from list: total=%d len=%d", total, len(got))
	}
	if got[0].TagNames == nil || len(got[0].TagNames) != 0 {
		t.Fatalf("business rule broken: legacy project tags = %v, want empty non-nil slice", got[0].TagNames)
	}

	detail, err := repo.FindByID(legacy.ID)
	if err != nil {
		t.Fatalf("business rule broken: legacy project detail failed: %v", err)
	}
	if detail.TagNames == nil || len(detail.TagNames) != 0 {
		t.Fatalf("business rule broken: legacy detail tags = %v, want empty non-nil slice", detail.TagNames)
	}
}

// TestRegressionPaginationBoundaries 分页边界：末页不满、整页恰好分完、超出末页返回空但 total 不变。
func TestRegressionPaginationBoundaries(t *testing.T) {
	_, repo := newRegressionDB(t, "")
	const n = 5
	for i := 0; i < n; i++ {
		seedProject(t, repo, "分页项目", "受访者", "draft", nil)
	}

	t.Run("last partial page", func(t *testing.T) {
		// page_size=2：前 2 页各 2 条，第 3 页 1 条，第 4 页越界为空；total 始终为 5。
		var pageLens []int
		for page := 1; page <= 4; page++ {
			got, total, err := repo.List(page, 2, ProjectListFilter{})
			if err != nil {
				t.Fatalf("business rule broken: page %d query failed: %v", page, err)
			}
			if total != n {
				t.Fatalf("business rule broken: total changed across pages = %d, want %d", total, n)
			}
			pageLens = append(pageLens, len(got))
		}
		wantLens := []int{2, 2, 1, 0}
		for i := range wantLens {
			if pageLens[i] != wantLens[i] {
				t.Fatalf("business rule broken: page lens = %v, want %v (page %d)", pageLens, wantLens, i+1)
			}
		}
	})

	t.Run("exact full page then empty", func(t *testing.T) {
		// 正好整页：page_size=5 第 1 页 5 条，第 2 页为空而不是重复返回。
		page1, total, err := repo.List(1, n, ProjectListFilter{})
		if err != nil {
			t.Fatalf("page 1 failed: %v", err)
		}
		if total != n || len(page1) != n {
			t.Fatalf("business rule broken: exact full page len=%d total=%d, want %d/%d", len(page1), total, n, n)
		}
		ids := map[uint]bool{}
		for _, p := range page1 {
			if ids[p.ID] {
				t.Fatalf("business rule broken: duplicated project id %d on a page", p.ID)
			}
			ids[p.ID] = true
		}
		page2, _, err := repo.List(2, n, ProjectListFilter{})
		if err != nil {
			t.Fatalf("page 2 failed: %v", err)
		}
		if len(page2) != 0 {
			t.Fatalf("business rule broken: page beyond last must be empty, got %d rows", len(page2))
		}
	})

	t.Run("filter and pagination compose", func(t *testing.T) {
		// 筛选 + 分页组合：标签命中的项目跨页可取全。
		seedProject(t, repo, "标签分页一", "受访者", "draft", []string{"页标"})
		seedProject(t, repo, "标签分页二", "受访者", "draft", []string{"页标"})
		seedProject(t, repo, "标签分页三", "受访者", "draft", []string{"页标"})

		got1, total, err := repo.List(1, 2, ProjectListFilter{Tags: []string{"页标"}})
		if err != nil {
			t.Fatalf("filtered page 1 failed: %v", err)
		}
		if total != 3 || len(got1) != 2 {
			t.Fatalf("business rule broken: filtered page1 len=%d total=%d, want 2/3", len(got1), total)
		}
		got2, _, err := repo.List(2, 2, ProjectListFilter{Tags: []string{"页标"}})
		if err != nil {
			t.Fatalf("filtered page 2 failed: %v", err)
		}
		if len(got2) != 1 || got2[0].Title != "标签分页一" {
			t.Fatalf("business rule broken: filtered page2 = %v, want only 标签分页一 (id DESC order)", got2)
		}
	})
}
