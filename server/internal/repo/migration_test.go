package repo

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/compforge/loopd/server/internal/migrations"
	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/gorm"
)

func TestMessageSchemaRequiresExplicitRecreation(t *testing.T) {
	for _, column := range []string{"purpose", "kind", "actor_key", "sender_key", "key"} {
		t.Run(column, func(t *testing.T) {
			s := openTestStore(t)
			if err := migrations.CurrentSchema(s.db); err != nil {
				t.Fatalf("current schema rejected: %v", err)
			}
			// Only the test-owned SQLite database is altered; startup must never
			// silently rename or drop columns from an existing deployment.
			if err := s.db.Exec(`ALTER TABLE messages ADD COLUMN "` + column + `" VARCHAR(128)`).Error; err != nil {
				t.Fatal(err)
			}
			if err := migrations.CurrentSchema(s.db); err == nil || !strings.Contains(err.Error(), "messages."+column) {
				t.Fatalf("obsolete schema accepted: %v", err)
			}
		})
	}
}

func TestOpenMigratesDomainKeys(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		testOpenMigratesDomainKeys(t, Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "legacy.db")})
	})
	t.Run("mysql", func(t *testing.T) {
		dsn := os.Getenv("TEST_MYSQL_MIGRATION_DSN")
		if dsn == "" {
			t.Skip("set TEST_MYSQL_MIGRATION_DSN to an empty disposable database")
		}
		testOpenMigratesDomainKeys(t, Config{Driver: "mysql", DSN: dsn})
	})
}

func TestMessagePartSchemaRequiresExplicitRecreation(t *testing.T) {
	s := openTestStore(t)
	// This database belongs to the test. Production startup only inspects schema.
	if err := s.db.Migrator().DropColumn(&model.MessagePart{}, "group_id"); err != nil {
		t.Fatal(err)
	}
	if err := migrations.CurrentSchema(s.db); err == nil || !strings.Contains(err.Error(), "message_parts") {
		t.Fatalf("accepted incompatible Part schema: %v", err)
	}
	if s.db.Migrator().HasColumn(&model.MessagePart{}, "group_id") {
		t.Fatal("schema check mutated the database")
	}
}

func testOpenMigratesDomainKeys(t *testing.T, config Config) {
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
	if err := db.AutoMigrate(&model.Conversation{}, &model.Message{}, &legacyOperator{}, &legacyHarness{}); err != nil {
		t.Fatal(err)
	}
	content := []byte(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`)
	expires := time.Now().UTC().Add(time.Hour)
	for _, record := range []any{
		&model.Conversation{ID: "conversation-1", Name: "Preserved"},
		&model.Message{ID: "message-1", ConversationID: "conversation-1", TaskID: "task-1", SourceKind: "user", SourceKey: "user-1", Content: content},
		&legacyOperator{ID: "operator-1", Key: "router", ExpiresAt: expires},
		&legacyHarness{ID: "harness-1", Key: "agentd", ExpiresAt: expires},
	} {
		if err := db.Create(record).Error; err != nil {
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
		for table, column := range map[string]string{"messages": "source_key", "operators": "operator_key", "harnesses": "harness_key"} {
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
		if err != nil || len(messages) != 1 || messages[0].ID != "message-1" || messages[0].SourceKey != "user-1" {
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
