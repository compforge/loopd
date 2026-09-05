package repo

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/compforge/loopd/server/internal/model"
	"github.com/qiankunli/go-stdx/uuid"
)

func TestRegistryRoundTrip(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) { testRegistryRoundTrip(t, openTestStore(t)) })
	t.Run("mysql", func(t *testing.T) {
		dsn := os.Getenv("TEST_MYSQL_DSN")
		if dsn == "" {
			t.Skip("set TEST_MYSQL_DSN to run the MySQL integration test")
		}
		store, err := Open(Config{Driver: "mysql", DSN: dsn})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		testRegistryRoundTrip(t, store)
	})
}

func testRegistryRoundTrip(t *testing.T, store *Store) {
	t.Helper()
	// Roll back all fixtures, including failures, without deleting shared data.
	tx := store.db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	t.Cleanup(func() { _ = tx.Rollback().Error })
	store = &Store{db: tx, operationTimeout: store.operationTimeout}
	ctx := context.Background()
	expires := time.Now().UTC().Add(time.Minute)
	key := "registry-test-" + uuid.V7()
	operator, err := store.RegisterOperator(ctx, model.Operator{ID: uuid.V7(), OperatorKey: key, ExpiresAt: expires})
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := store.RegisterOperator(ctx, model.Operator{ID: uuid.V7(), OperatorKey: key, DisplayName: "renewed", ExpiresAt: expires})
	if err != nil || renewed.ID != operator.ID || renewed.DisplayName != "renewed" {
		t.Fatalf("Operator renewal = %#v, error = %v", renewed, err)
	}
	operators, err := store.ListOperators(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for i, item := range operators {
		found = found || item.ID == operator.ID
		if i > 0 && operators[i-1].OperatorKey > item.OperatorKey {
			t.Fatal("Operators are not ordered by key")
		}
	}
	if !found {
		t.Fatal("registered Operator missing from discovery")
	}
	harness, err := store.RegisterHarness(ctx, model.Harness{ID: uuid.V7(), HarnessKey: key, ExpiresAt: expires})
	if err != nil {
		t.Fatal(err)
	}
	renewedHarness, err := store.RegisterHarness(ctx, model.Harness{ID: uuid.V7(), HarnessKey: key, DisplayName: "renewed", ExpiresAt: expires})
	if err != nil || renewedHarness.ID != harness.ID || renewedHarness.DisplayName != "renewed" {
		t.Fatalf("Harness renewal = %#v, error = %v", renewedHarness, err)
	}
	harnesses, err := store.ListHarnesses(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for i, item := range harnesses {
		found = found || item.ID == harness.ID
		if i > 0 && harnesses[i-1].HarnessKey > item.HarnessKey {
			t.Fatal("Harnesses are not ordered by key")
		}
	}
	if !found {
		t.Fatal("registered Harness missing from discovery")
	}
}
