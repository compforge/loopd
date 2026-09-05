package migrations

import (
	"fmt"
	"log/slog"

	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/gorm"
)

// DomainKeys renames legacy columns in place, preserving values and constraints.
// Run before AutoMigrate, with older server instances stopped: both versions
// cannot write the same schema during this rename.
func DomainKeys(db *gorm.DB) error {
	migrator := db.Migrator()
	for _, change := range []struct {
		table     string
		model     any
		oldColumn string
		column    string
	}{
		{"messages", &model.Message{}, "key", "actor_key"},
		{"messages", &model.Message{}, "sender_key", "actor_key"},
		{"operators", &model.Operator{}, "key", "operator_key"},
		{"harnesses", &model.Harness{}, "key", "harness_key"},
	} {
		if !migrator.HasTable(change.model) {
			continue
		}
		// SQLite's HasColumn uses a SQL-text LIKE match: "key" can match
		// PRIMARY KEY. Inspect exact column names instead.
		columns, err := migrator.ColumnTypes(change.model)
		if err != nil {
			return fmt.Errorf("inspect %s columns: %w", change.table, err)
		}
		var hasOld, hasNew bool
		for _, column := range columns {
			hasOld = hasOld || column.Name() == change.oldColumn
			hasNew = hasNew || column.Name() == change.column
		}
		if hasOld {
			if hasNew {
				return fmt.Errorf("%s has both %s and %s; resolve the conflicting columns before startup", change.table, change.oldColumn, change.column)
			}
			// Supplying the model lets GORM use CHANGE COLUMN on older MySQL.
			if err := migrator.RenameColumn(change.model, change.oldColumn, change.column); err != nil {
				return fmt.Errorf("rename %s.%s to %s: %w", change.table, change.oldColumn, change.column, err)
			}
			slog.InfoContext(db.Statement.Context, "database column renamed", "table", change.table, "column", change.column)
		}
		oldIndex := "idx_" + change.table + "_" + change.oldColumn
		newIndex := "idx_" + change.table + "_" + change.column
		if migrator.HasIndex(change.model, oldIndex) && !migrator.HasIndex(change.model, newIndex) {
			if err := migrator.RenameIndex(change.model, oldIndex, newIndex); err != nil {
				return fmt.Errorf("rename %s index: %w", change.table, err)
			}
		}
	}
	return nil
}
