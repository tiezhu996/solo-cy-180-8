package database

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/oralhistory/oralhistory/internal/model"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// openFileDB 打开基于临时文件的 SQLite（WAL + busy_timeout），
// 关闭后重开同一文件即等价于“进程重启”，用于验证迁移的可恢复性。
func openFileDB(t *testing.T, file string) *gorm.DB {
	t.Helper()
	dsn := file + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open file db %s: %v", file, err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

// legacyProjectTagNoUnique 模拟标签表上线早期的形态：表存在但缺少唯一索引。
type legacyProjectTagNoUnique struct {
	ID        uint `gorm:"primaryKey"`
	ProjectID uint `gorm:"not null"`
	Name      string
}

func (legacyProjectTagNoUnique) TableName() string { return "project_tags" }

// TestMigrateRecoversAfterFailedMigration 迁移因脏数据失败后，修复数据并重启，
// 迁移必须能成功补齐唯一索引；这是“迁移中途失败再启动”的核心回归。
func TestMigrateRecoversAfterFailedMigration(t *testing.T) {
	file := t.TempDir() + "/recover.db"

	// 第一阶段：旧形态库——projects 表 + 无唯一约束的 project_tags，且已存在重复标签。
	db := openFileDB(t, file)
	if err := db.AutoMigrate(&model.User{}, &model.Project{}, &legacyProjectTagNoUnique{}); err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	project := model.Project{Title: "脏数据项目", IntervieweeName: "受访者", BirthYear: 1930, Status: "draft", CreatedBy: 1}
	if err := db.Create(&project).Error; err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if err := db.Create(&[]legacyProjectTagNoUnique{
		{ProjectID: project.ID, Name: "抗战"},
		{ProjectID: project.ID, Name: "抗战"}, // 违反即将建立的唯一索引的历史重复数据
	}).Error; err != nil {
		t.Fatalf("seed duplicate tags: %v", err)
	}
	if db.Migrator().HasIndex(&model.ProjectTag{}, "uk_project_tag_name") {
		t.Fatalf("precondition broken: unique index should not exist before migration")
	}

	// 第二阶段：首次启动执行迁移——存在重复数据时唯一索引创建必须明确失败，而不是静默跳过。
	// （MySQL 报 Error 1062 Duplicate entry，SQLite 报 UNIQUE constraint，措辞因驱动而异，
	// 故只判定业务规则：迁移返回错误且唯一索引未建成。）
	if err := Migrate(db); err == nil {
		t.Fatalf("business rule broken: migrate must FAIL when duplicate data blocks the unique index, got nil error")
	}
	if db.Migrator().HasIndex(&model.ProjectTag{}, "uk_project_tag_name") {
		t.Fatalf("business rule broken: unique index must not exist after failed migration")
	}
	sqlDB, _ := db.DB()
	_ = sqlDB.Close()

	// 第三阶段：运维清理重复数据后“重启”，迁移必须成功并把后续表与索引补齐。
	db = openFileDB(t, file)
	if err := db.Exec("DELETE FROM project_tags WHERE rowid NOT IN (SELECT MIN(rowid) FROM project_tags GROUP BY project_id, name)").Error; err != nil {
		t.Fatalf("dedupe dirty rows: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("business rule broken: migrate after fixing data must succeed on restart, got %v", err)
	}
	if !db.Migrator().HasIndex(&model.ProjectTag{}, "uk_project_tag_name") {
		t.Fatalf("business rule broken: unique index uk_project_tag_name must exist after successful restart migration")
	}
	for _, m := range migrationModels {
		if !db.Migrator().HasTable(m) {
			t.Fatalf("business rule broken: table for %T missing after recovery migration", m)
		}
	}
	// 已有数据保留：项目仍在，标签剩余 1 行且可正常读写。
	var kept model.Project
	if err := db.First(&kept, project.ID).Error; err != nil {
		t.Fatalf("business rule broken: existing project lost after recovery: %v", err)
	}
	var tagCount int64
	db.Model(&model.ProjectTag{}).Where("project_id = ?", project.ID).Count(&tagCount)
	if tagCount != 1 {
		t.Fatalf("business rule broken: deduped tag count = %d, want 1", tagCount)
	}

	// 第四阶段：再次重启、再次迁移必须幂等成功。
	sqlDB, _ = db.DB()
	_ = sqlDB.Close()
	db = openFileDB(t, file)
	if err := Migrate(db); err != nil {
		t.Fatalf("business rule broken: repeated migrate after recovery must be idempotent, got %v", err)
	}
}

// TestMigrateResumesAfterKilledMidway 模拟进程在迁移中途被杀（只建了部分表），
// 重启后迁移补齐剩余表，已有数据不丢。
func TestMigrateResumesAfterKilledMidway(t *testing.T) {
	cases := []struct {
		name string
		done []any // “被杀时”已经建好的表（按迁移清单的前缀）
	}{
		{name: "killed before project_tags", done: []any{&model.User{}, &model.Project{}}},
		{name: "killed after project_tags", done: []any{&model.User{}, &model.Project{}, &model.ProjectTag{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file := t.TempDir() + "/killed.db"

			// 进程第一次启动，迁移到一半被杀：只留下前缀表与一条业务数据。
			db := openFileDB(t, file)
			if err := db.AutoMigrate(tc.done...); err != nil {
				t.Fatalf("seed partial schema: %v", err)
			}
			project := model.Project{Title: "中断前的项目", IntervieweeName: "老爷爷", BirthYear: 1928, Status: "completed", CreatedBy: 1}
			if err := db.Create(&project).Error; err != nil {
				t.Fatalf("seed project before kill: %v", err)
			}
			sqlDB, _ := db.DB()
			_ = sqlDB.Close()

			// 进程重启：执行完整迁移，必须补齐缺失表且不破坏数据。
			db = openFileDB(t, file)
			if err := Migrate(db); err != nil {
				t.Fatalf("business rule broken: resume migration after kill must succeed, got %v", err)
			}
			for _, m := range migrationModels {
				if !db.Migrator().HasTable(m) {
					t.Fatalf("business rule broken: table for %T missing after resume", m)
				}
			}
			var kept model.Project
			if err := db.First(&kept, project.ID).Error; err != nil {
				t.Fatalf("business rule broken: project created before kill lost after resume: %v", err)
			}
			if kept.Title != "中断前的项目" || kept.Status != "completed" {
				t.Fatalf("business rule broken: existing row altered after resume: %+v", kept)
			}

			// 旧项目可以补录标签——验证升级后功能真正可用。
			if err := db.Create(&model.ProjectTag{ProjectID: project.ID, Name: "补录标签"}).Error; err != nil {
				t.Fatalf("business rule broken: cannot add tag to pre-kill project after resume: %v", err)
			}

			// 再重启再迁移：幂等。
			sqlDB, _ = db.DB()
			_ = sqlDB.Close()
			db = openFileDB(t, file)
			if err := Migrate(db); err != nil {
				t.Fatalf("business rule broken: second resume migration must be idempotent, got %v", err)
			}
			var tags int64
			db.Model(&model.ProjectTag{}).Where("project_id = ?", project.ID).Count(&tags)
			if tags != 1 {
				t.Fatalf("business rule broken: tag count = %d after second resume, want 1", tags)
			}
		})
	}
}
