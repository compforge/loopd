package migrations

import (
	"fmt"
	"gorm.io/gorm"
)

// CurrentSchema refuses incompatible development schemas before AutoMigrate.
// Recreating a database is an explicit deployment action, never a startup side effect.
func CurrentSchema(db *gorm.DB) error {
	if db.Migrator().HasTable("message_parts") && !db.Migrator().HasColumn("message_parts", "group_id") {
		return fmt.Errorf("incompatible development schema message_parts: explicitly recreate the development database before starting this version")
	}
	for table, obsolete := range map[string][]string{
		"conversations": {"parent_message_id", "task_id", "parent_conversation_id"},
		"messages":      {"reply_to_message_id", "purpose", "kind", "actor_key", "key", "sender_key"},
	} {
		if !db.Migrator().HasTable(table) {
			continue
		}
		columns, err := db.Migrator().ColumnTypes(table)
		if err != nil {
			return err
		}
		for _, column := range columns {
			for _, name := range obsolete {
				if column.Name() == name {
					return fmt.Errorf("incompatible development schema %s.%s: explicitly recreate the development database before starting this version", table, name)
				}
			}
		}
	}
	return nil
}
