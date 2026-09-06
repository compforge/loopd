package server

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/compforge/loopd/pkg/contract"
)

func TestNewConnectsConfiguredRedis(t *testing.T) {
	redisServer := miniredis.RunT(t)
	server, err := New(Config{
		Database:      DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "loopd.db")},
		Redis:         RedisConfig{Address: redisServer.Addr()},
		Conversations: testConversations{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
}

type testConversations struct{}

func (testConversations) Signal(context.Context, string, string, contract.ActorRef, uint64) error {
	return nil
}
func (testConversations) Poll(context.Context, string, contract.ActorRef, string, func(context.Context, string) ([]contract.Message, error)) (contract.PollResult, error) {
	return contract.PollResult{}, nil
}

func (testConversations) Commit(context.Context, string, contract.CommitRequest) error { return nil }
