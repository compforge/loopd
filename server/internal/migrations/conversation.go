package migrations

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/gorm"
)

// ConversationOwnership runs after AutoMigrate adds the new columns. Stop old
// writers before startup: parent_message_id and task_id are different relations,
// not interchangeable column names. Retrying a partially applied migration is safe.
func ConversationOwnership(db *gorm.DB) error {
	if !db.Migrator().HasColumn("conversations", "parent_message_id") {
		return nil
	}
	// Keep the legacy shape local to migration code, including for SQLite's
	// table rebuild when the obsolete column is dropped.
	type legacyConversation struct {
		model.Conversation
		ParentMessageID *string
	}
	var after string
	for {
		var rows []legacyConversation
		if err := db.Table("conversations").Where("id > ?", after).Order("id ASC").Limit(100).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			updates := map[string]any{}
			if row.ParentMessageID != nil {
				var parent model.Message
				if err := db.First(&parent, "id = ?", *row.ParentMessageID).Error; err != nil {
					return fmt.Errorf("resolve work conversation %s: %w", row.ID, err)
				}
				if parent.TaskID == "" || (parent.Kind != "operator" && parent.Kind != "harness") {
					return fmt.Errorf("work conversation %s has no valid task target", row.ID)
				}
				updates["task_id"], updates["actor_kind"], updates["actor_key"] = parent.TaskID, parent.Kind, parent.ActorKey
			} else {
				updates["actor_kind"] = "user"
				var first model.Message
				err := db.Where("conversation_id = ? AND kind = ?", row.ID, "user").Order("id ASC").First(&first).Error
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				// An empty legacy conversation has no recoverable user identity.
				// Leave its key empty rather than inventing a principal.
				if err == nil {
					updates["actor_key"] = first.ActorKey
				}
			}
			if err := db.Model(&model.Conversation{}).Where("id = ?", row.ID).UpdateColumns(updates).Error; err != nil {
				return err
			}
		}
		after = rows[len(rows)-1].ID
	}
	migrator := db.Table("conversations").Migrator()
	if migrator.HasIndex(&legacyConversation{}, "idx_conversations_parent_message_id") {
		if err := migrator.DropIndex(&legacyConversation{}, "idx_conversations_parent_message_id"); err != nil {
			return err
		}
	}
	if err := migrator.DropColumn(&legacyConversation{}, "parent_message_id"); err != nil {
		return err
	}
	// SQLite drops secondary indexes when GORM rebuilds the table for
	// DropColumn. Restore the task constraint before accepting new writes.
	if !db.Migrator().HasIndex(&model.Conversation{}, "idx_conversations_task_id") {
		if err := db.Migrator().CreateIndex(&model.Conversation{}, "idx_conversations_task_id"); err != nil {
			return err
		}
	}
	slog.InfoContext(db.Statement.Context, "conversation ownership migrated to actor and task")
	return nil
}
