package repo

import (
	"context"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/compforge/loopd/server/internal/model"
)

func TestOpenCreatesChatAndActorTables(t *testing.T) {
	store := openTestStore(t)

	var tables []string
	if err := store.db.Raw(
		"SELECT name FROM sqlite_master WHERE type = ? AND name NOT LIKE ?",
		"table", "sqlite_%",
	).Scan(&tables).Error; err != nil {
		t.Fatal(err)
	}
	sort.Strings(tables)
	if want := []string{"conversations", "harnesses", "messages", "operators"}; !reflect.DeepEqual(tables, want) {
		t.Fatalf("tables = %v, want %v", tables, want)
	}
	for _, table := range tables {
		assertPrimaryKey(t, store, table, "id")
	}
}

func TestOpenRejectsInvalidMySQLDSN(t *testing.T) {
	_, err := Open(Config{Driver: "mysql", DSN: "://invalid"})
	if err == nil {
		t.Fatal("Open succeeded with an invalid MySQL DSN")
	}
	if !strings.Contains(err.Error(), "parse loopd MySQL DSN") {
		t.Fatalf("Open error = %v, want MySQL DSN parse error", err)
	}
}

func TestMessagesUseUUIDOrderAsCursor(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: "01991f3d-1111-7000-8000-000000000000"}); err != nil {
		t.Fatal(err)
	}
	first := model.Message{
		ID: "01991f3d-1112-7000-8000-000000000000", ConversationID: "01991f3d-1111-7000-8000-000000000000",
		TaskID: "01991f3d-1110-7000-8000-000000000000", Kind: "user", ActorKey: "user-1",
		Content: []byte(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`),
	}
	second := model.Message{
		ID: "01991f3d-1113-7000-8000-000000000000", ConversationID: first.ConversationID,
		TaskID: first.TaskID, Kind: "operator", ActorKey: "operator-1",
		Content: []byte(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`),
	}
	if _, err := store.CreateMessage(ctx, second); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateMessage(ctx, first); err != nil {
		t.Fatal(err)
	}

	messages, err := store.ListMessages(ctx, first.ConversationID, first.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].ID != second.ID {
		t.Fatalf("messages after %s = %#v", first.ID, messages)
	}
}

func TestListConversationsReturnsOnlyRootsNewestFirst(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	first := model.Conversation{ID: "01991f3d-1111-7000-8000-000000000000", Name: "First"}
	second := model.Conversation{ID: "01991f3d-1112-7000-8000-000000000000", Name: "Second"}
	parentID := first.ID
	detail := model.Conversation{
		ID: "01991f3d-1114-7000-8000-000000000000", Name: "Detail", ParentID: &parentID, ActorKind: "operator", ActorKey: "router",
	}
	for _, conversation := range []model.Conversation{first, second, detail} {
		if _, err := store.CreateConversation(ctx, conversation); err != nil {
			t.Fatal(err)
		}
	}

	conversations, err := store.ListConversations(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversations) != 2 || conversations[0].ID != second.ID || conversations[1].ID != first.ID {
		t.Fatalf("conversations = %#v", conversations)
	}
	before, err := store.ListConversations(ctx, second.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 || before[0].ID != first.ID {
		t.Fatalf("conversations before %s = %#v", second.ID, before)
	}
}

func TestCreateChatInputRejectsMissingConversation(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	conversationID := "01991f3d-1111-7000-8000-000000000000"
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: conversationID}); err != nil {
		t.Fatal(err)
	}
	existing := model.Message{
		ID: "01991f3d-1112-7000-8000-000000000000", ConversationID: conversationID,
		TaskID: "01991f3d-1110-7000-8000-000000000000", Kind: "operator", ActorKey: "existing",
		Content: []byte(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`),
	}
	if _, err := store.CreateMessage(ctx, existing); err != nil {
		t.Fatal(err)
	}
	user := model.Message{
		ID: "01991f3d-1113-7000-8000-000000000000", ConversationID: conversationID,
		TaskID: "01991f3d-1114-7000-8000-000000000000", Kind: "user", ActorKey: "user-1",
		Content: []byte(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`),
	}
	user.ConversationID = "missing"
	if _, err := store.CreateChatInput(ctx, user); err == nil {
		t.Fatal("CreateChatInput succeeded for a missing conversation")
	}
	messages, err := store.ListMessages(ctx, conversationID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].ID != existing.ID {
		t.Fatalf("transaction left partial messages: %#v", messages)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "loopd.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return store
}

func assertPrimaryKey(t *testing.T, store *Store, table, column string) {
	t.Helper()
	var columns []struct {
		Name string `gorm:"column:name"`
		PK   int    `gorm:"column:pk"`
	}
	if err := store.db.Raw("PRAGMA table_info(" + table + ")").Scan(&columns).Error; err != nil {
		t.Fatal(err)
	}
	for _, value := range columns {
		if value.Name == column && value.PK == 1 {
			return
		}
	}
	t.Fatalf("%s.%s is not the primary key: %#v", table, column, columns)
}
