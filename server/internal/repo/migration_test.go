package repo

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/gorm"
)

func TestOpenMigratesDomainKeys(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		testOpenMigratesDomainKeys(t, Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "legacy.db")}, "key")
	})
	t.Run("mysql", func(t *testing.T) {
		dsn := os.Getenv("TEST_MYSQL_MIGRATION_DSN")
		if dsn == "" {
			t.Skip("set TEST_MYSQL_MIGRATION_DSN to an empty disposable database")
		}
		testOpenMigratesDomainKeys(t, Config{Driver: "mysql", DSN: dsn}, "key")
	})
}

func TestOpenMigratesSenderKey(t *testing.T) {
	testOpenMigratesDomainKeys(t, Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "sender.db")}, "sender_key")
}

func testOpenMigratesDomainKeys(t *testing.T, config Config, messageColumn string) {
	t.Helper()
	config.OperationTimeout = 10 * time.Second
	dialector, _, err := databaseDialector(config)
	if err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(dialector, &gorm.Config{})
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
	// Never rewrite an existing deployment's schema for an integration test.
	for _, table := range []string{"conversations", "messages", "operators", "harnesses"} {
		if db.Migrator().HasTable(table) {
			t.Fatalf("migration test requires an empty disposable database; %s already exists", table)
		}
	}
	if err := db.AutoMigrate(&model.Conversation{}, &legacyMessage{}, &legacyOperator{}, &legacyHarness{}); err != nil {
		t.Fatal(err)
	}
	content := []byte(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`)
	expires := time.Now().UTC().Add(time.Hour)
	for _, record := range []any{
		&model.Conversation{ID: "conversation-1", Name: "Preserved"},
		&legacyMessage{ID: "message-1", ConversationID: "conversation-1", TaskID: "task-1", Kind: "user", Key: "user-1", Content: content},
		&legacyOperator{ID: "operator-1", Key: "router", ExpiresAt: expires},
		&legacyHarness{ID: "harness-1", Key: "agentd", ExpiresAt: expires},
	} {
		if err := db.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	if messageColumn != "key" {
		if err := db.Migrator().RenameColumn(&legacyMessage{}, "key", messageColumn); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}

	// A second open verifies the migration is safe to repeat after a restart.
	for attempt := 0; attempt < 2; attempt++ {
		store, err := Open(config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		for table, column := range map[string]string{"messages": "actor_key", "operators": "operator_key", "harnesses": "harness_key"} {
			columns, err := store.db.Migrator().ColumnTypes(table)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, value := range columns {
				if value.Name() == "key" || value.Name() == "sender_key" {
					t.Fatalf("%s still has a legacy %s column", table, value.Name())
				}
				found = found || value.Name() == column
			}
			if !found {
				t.Fatalf("%s columns were not renamed", table)
			}
		}
		messages, err := store.ListMessages(ctx, "conversation-1", "", 100)
		if err != nil || len(messages) != 1 || messages[0].ID != "message-1" || messages[0].ActorKey != "user-1" {
			t.Fatalf("migrated messages = %#v, error = %v", messages, err)
		}
		// MySQL may reformat JSON; compare the visible semantic content.
		var gotContent, wantContent any
		if err := json.Unmarshal(messages[0].Content, &gotContent); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(content, &wantContent); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotContent, wantContent) {
			t.Fatalf("message content changed: %s", messages[0].Content)
		}
		operator, err := store.RegisterOperator(ctx, model.Operator{ID: "new-operator", OperatorKey: "router", ExpiresAt: expires})
		if err != nil || operator.ID != "operator-1" {
			t.Fatalf("migrated operator = %#v, error = %v", operator, err)
		}
		harness, err := store.RegisterHarness(ctx, model.Harness{ID: "new-harness", HarnessKey: "agentd", ExpiresAt: expires})
		if err != nil || harness.ID != "harness-1" {
			t.Fatalf("migrated harness = %#v, error = %v", harness, err)
		}
		for _, duplicate := range []any{
			&model.Operator{ID: "duplicate", OperatorKey: "router", ExpiresAt: expires},
			&model.Harness{ID: "duplicate", HarnessKey: "agentd", ExpiresAt: expires},
		} {
			if err := store.db.WithContext(ctx).Create(duplicate).Error; err == nil {
				t.Fatal("domain key uniqueness was lost")
			}
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// These fixtures retain the previous schema independently of current models.
type legacyMessage struct {
	ID             string `gorm:"primaryKey;size:36"`
	ConversationID string `gorm:"size:36;not null;index:idx_message_conversation"`
	TaskID         string `gorm:"size:36;not null;index:idx_message_task"`
	Kind           string `gorm:"size:16;not null"`
	Key            string `gorm:"size:128;not null"`
	Content        []byte `gorm:"type:json;not null"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (legacyMessage) TableName() string { return "messages" }

type legacyOperator struct {
	ID          string `gorm:"primaryKey;size:36"`
	Key         string `gorm:"size:128;not null;uniqueIndex"`
	DisplayName string
	Description string
	ExpiresAt   time.Time `gorm:"not null;index"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (legacyOperator) TableName() string { return "operators" }

type legacyHarness legacyOperator

func (legacyHarness) TableName() string { return "harnesses" }
