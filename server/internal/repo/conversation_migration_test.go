package repo

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/gorm"
)

type legacyConversation struct {
	ID              string `gorm:"primaryKey;size:36"`
	Name            string
	ParentMessageID *string `gorm:"size:36;uniqueIndex"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (legacyConversation) TableName() string { return "conversations" }

func TestOpenMigratesConversationOwnership(t *testing.T) {
	config := Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "legacy-conversations.db")}
	dialect, _, err := databaseDialector(config)
	if err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(dialect, &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db = db.WithContext(ctx)
	if err := db.AutoMigrate(&legacyConversation{}, &model.Message{}); err != nil {
		t.Fatal(err)
	}
	parentID := "response"
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, value := range []any{
		&legacyConversation{ID: "root", Name: "Main", CreatedAt: at, UpdatedAt: at},
		&legacyConversation{ID: "empty", Name: "Empty", CreatedAt: at, UpdatedAt: at},
		&legacyConversation{ID: "detail", Name: "Work", ParentMessageID: &parentID, CreatedAt: at, UpdatedAt: at},
		&model.Message{ID: "input", ConversationID: "root", TaskID: "task", Kind: "user", ActorKey: "alice", Content: []byte(`{}`)},
		&model.Message{ID: parentID, ConversationID: "root", TaskID: "task", Kind: "operator", ActorKey: "router", Content: []byte(`{}`)},
		&model.Message{ID: "work", ConversationID: "detail", TaskID: "task", Kind: "harness", ActorKey: "call", Content: []byte(`{"text":"preserved"}`)},
	} {
		if err := db.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		store, err := Open(config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		if store.db.Migrator().HasColumn("conversations", "parent_message_id") {
			t.Fatal("legacy parent column remains")
		}
		root, err := store.GetConversation(ctx, "root")
		if err != nil || root.ActorKind != "user" || root.ActorKey != "alice" || root.TaskID != nil || !root.UpdatedAt.Equal(at) {
			t.Fatalf("root = %+v, error = %v", root, err)
		}
		detail, err := store.FindConversationByTask(ctx, "task")
		if err != nil || detail.ID != "detail" || detail.ActorKind != "operator" || detail.ActorKey != "router" || !detail.UpdatedAt.Equal(at) {
			t.Fatalf("detail = %+v, error = %v", detail, err)
		}
		empty, err := store.GetConversation(ctx, "empty")
		if err != nil || empty.ActorKey != "" || empty.ActorKind != "user" {
			t.Fatalf("empty = %+v, error = %v", empty, err)
		}
		messages, err := store.ListMessagesByTask(ctx, "task", "", 100)
		if err != nil || len(messages) != 3 || string(messages[2].Content) != `{"text":"preserved"}` {
			t.Fatalf("messages = %+v, error = %v", messages, err)
		}
		if _, err := store.CreateConversation(ctx, model.Conversation{ID: "duplicate", TaskID: detail.TaskID, ActorKind: "operator", ActorKey: "router"}); err == nil {
			t.Fatal("task uniqueness lost")
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
