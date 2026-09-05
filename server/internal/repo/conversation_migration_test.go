package repo

import (
	"gorm.io/gorm"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenRejectsObsoleteConversationSchemaWithoutChangingData(t *testing.T) {
	config := Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "obsolete.db")}
	dialect, _, err := databaseDialector(config)
	if err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(dialect, &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := db.DB()
	defer pool.Close()
	if err := db.Exec("CREATE TABLE conversations (id TEXT PRIMARY KEY, task_id TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO conversations (id, task_id) VALUES (?, ?)", "kept", "legacy").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := Open(config); err == nil || !strings.Contains(err.Error(), "incompatible development schema") {
		t.Fatalf("open: %v", err)
	}
	var count int64
	if err := db.Table("conversations").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("data changed: %d %v", count, err)
	}
}
