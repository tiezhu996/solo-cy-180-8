package database

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/oralhistory/oralhistory/internal/model"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func newSQLiteDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared&_pragma=foreign_keys(1)"), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// 每个测试使用独立的内存库，避免共享缓存相互污染。
	if err := db.Exec("DROP TABLE IF EXISTS project_tags; DROP TABLE IF EXISTS timeline_markers; DROP TABLE IF EXISTS recordings; DROP TABLE IF EXISTS questions; DROP TABLE IF EXISTS audit_logs; DROP TABLE IF EXISTS projects; DROP TABLE IF EXISTS users;").Error; err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	return db
}

// TestMigrateFreshEnvironment 空环境：Migrate 必须自动建立标签表在内的全部表。
func TestMigrateFreshEnvironment(t *testing.T) {
	db := newSQLiteDB(t)

	if err := Migrate(db); err != nil {
		t.Fatalf("fresh migrate: %v", err)
	}
	for _, m := range migrationModels {
		if !db.Migrator().HasTable(m) {
			t.Fatalf("table for %T not created in fresh environment", m)
		}
	}
	if !db.Migrator().HasTable(&model.ProjectTag{}) {
		t.Fatalf("project_tags table missing after fresh migrate")
	}

	// 建表后应能直接写入项目与标签。
	project := model.Project{
		Title: "空环境项目", IntervieweeName: "受访者", BirthYear: 1940,
		Status: "draft", CreatedBy: 1, TagNames: []string{"抗战", "知青"},
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatalf("create project: %v", err)
	}
	tags := []model.ProjectTag{
		{ProjectID: project.ID, Name: "抗战"},
		{ProjectID: project.ID, Name: "知青"},
	}
	if err := db.Create(&tags).Error; err != nil {
		t.Fatalf("create tags: %v", err)
	}

	// 再次执行迁移应幂等，不报错也不丢数据。
	if err := Migrate(db); err != nil {
		t.Fatalf("idempotent migrate: %v", err)
	}
	var tagCount int64
	if err := db.Model(&model.ProjectTag{}).Count(&tagCount).Error; err != nil {
		t.Fatalf("count tags: %v", err)
	}
	if tagCount != 2 {
		t.Fatalf("tag count = %d after re-migrate, want 2", tagCount)
	}
}

// TestMigrateUpgradeFromLegacy 已有数据升级：旧库没有 project_tags，
// 迁移后自动补建标签表，存量项目保持完好且可补录标签。
func TestMigrateUpgradeFromLegacy(t *testing.T) {
	db := newSQLiteDB(t)

	// 模拟旧版本：只迁移标签功能上线之前的表。
	legacyModels := []any{
		&model.User{},
		&model.Project{},
		&model.Question{},
		&model.Recording{},
		&model.TimelineMarker{},
		&model.AuditLog{},
	}
	if err := db.AutoMigrate(legacyModels...); err != nil {
		t.Fatalf("legacy migrate: %v", err)
	}
	if db.Migrator().HasTable(&model.ProjectTag{}) {
		t.Fatalf("legacy database should not have project_tags before upgrade")
	}

	legacy := model.Project{
		Title: "老项目", IntervieweeName: "老爷爷", BirthYear: 1928,
		Status: "completed", CreatedBy: 1,
	}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatalf("seed legacy project: %v", err)
	}

	// 升级：执行当前版本迁移。
	if err := Migrate(db); err != nil {
		t.Fatalf("upgrade migrate: %v", err)
	}
	if !db.Migrator().HasTable(&model.ProjectTag{}) {
		t.Fatalf("project_tags must be auto-created when upgrading legacy database")
	}

	// 存量项目不受影响。
	var got model.Project
	if err := db.First(&got, legacy.ID).Error; err != nil {
		t.Fatalf("load legacy project after upgrade: %v", err)
	}
	if got.Title != "老项目" || got.Status != "completed" {
		t.Fatalf("legacy project altered: %+v", got)
	}

	// 旧项目可以补录标签。
	upgraded := []model.ProjectTag{{ProjectID: legacy.ID, Name: "渡江战役"}}
	if err := db.Create(&upgraded).Error; err != nil {
		t.Fatalf("add tags to legacy project after upgrade: %v", err)
	}

	// 唯一约束生效：同一项目重复标签不允许写入。
	dup := model.ProjectTag{ProjectID: legacy.ID, Name: "渡江战役"}
	if err := db.Create(&dup).Error; err == nil {
		t.Fatalf("duplicate tag should violate unique index")
	}
}
