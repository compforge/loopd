package repo

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// +case=`Message status does not filter Poll; the Operator owns readiness and Commit decisions.`
func TestInboxReturnsEveryStatus(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.CreateConversation(ctx, model.Conversation{ID: "conv"}); err != nil {
		t.Fatal(err)
	}
	statuses := []contract.MessageStatus{contract.MessageStatusStreaming, contract.MessageStatusCompleted, contract.MessageStatusFailed, contract.MessageStatusCancelled, contract.MessageStatusExpired}
	for _, status := range statuses {
		_, err := s.CreateMessage(ctx, model.Message{ID: string(status), ConversationID: "conv", SourceKind: "operator", SourceKey: "writer", Status: string(status), Content: []byte(`{"version":"1.1","biz":"chat","meta":{},"blocks":[]}`)})
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.ListInbox(ctx, "conv", "operator", "reader", "", 100)
	if err != nil || len(rows) != len(statuses) {
		t.Fatalf("all-status inbox=%+v err=%v", rows, err)
	}
}

// All schema and data changes are confined to a test-owned temporary database.
type messageBeforeRecipients struct {
	ID             string `gorm:"primaryKey;size:36"`
	ConversationID string `gorm:"size:36;not null"`
	TaskID         string `gorm:"size:36;not null"`
	SourceKind     string `gorm:"size:16;not null"`
	SourceKey      string `gorm:"size:128;not null"`
	Content        []byte `gorm:"type:json;not null"`
}

func (messageBeforeRecipients) TableName() string { return "messages" }

func TestRecipientMigrationDoesNotBroadcastHistoricalMessages(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "migration.db")
	old, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := old.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if err := old.AutoMigrate(&messageBeforeRecipients{}); err != nil {
		t.Fatal(err)
	}
	content := []byte(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`)
	if err := old.Create(&messageBeforeRecipients{
		ID: "001", ConversationID: "conv", TaskID: "old-chat", SourceKind: "user", SourceKey: "alice", Content: content,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(Config{Driver: "sqlite", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	inbox, err := store.ListInbox(ctx, "conv", "operator", "router", "", 100)
	if err != nil || len(inbox) != 0 {
		t.Fatalf("historical rows became broadcasts: %+v, %v", inbox, err)
	}
	history, err := store.ListMessages(ctx, "conv", "", 100)
	if err != nil || len(history) != 1 {
		t.Fatalf("migration changed visible history: %+v, %v", history, err)
	}
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: "conv", ActorKind: "user", ActorKey: "alice"}); err != nil {
		t.Fatal(err)
	}
	// New empty recipients are intentionally written as empty strings, not NULL.
	if _, err := store.CreateMessage(ctx, model.Message{
		ID: "002", ConversationID: "conv", TaskID: "new-chat", SourceKind: "user", SourceKey: "alice", Content: content,
	}); err != nil {
		t.Fatal(err)
	}
	inbox, err = store.ListInbox(ctx, "conv", "operator", "router", "", 100)
	if err != nil || len(inbox) != 1 || inbox[0].ID != "002" {
		t.Fatalf("explicit broadcast = %+v, %v", inbox, err)
	}
}
