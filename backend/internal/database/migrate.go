package database

import (
	"fmt"

	"github.com/oralhistory/oralhistory/internal/model"
	"gorm.io/gorm"
)

// migrationModels 是全库唯一的 AutoMigrate 表清单（单一事实来源）。
// 新增实体时只需要在这里登记，database.New 启动迁移与迁移测试都复用它，
// 避免出现多份清单导致空环境/升级环境漏建表。
var migrationModels = []any{
	&model.User{},
	&model.Project{},
	&model.ProjectTag{},
	&model.Question{},
	&model.Recording{},
	&model.TimelineMarker{},
	&model.AuditLog{},
}

// Migrate 执行表结构迁移；空环境会新建全部表，已有数据环境只做增量变更，不影响存量数据。
func Migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(migrationModels...); err != nil {
		return fmt.Errorf("auto migrate: %w", err)
	}
	return nil
}
