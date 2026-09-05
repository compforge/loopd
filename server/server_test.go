package server

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alicebob/miniredis/v2"
	loopd "github.com/compforge/loopd"
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

func (testConversations) Signal(context.Context, string, string, loopd.ActorRef) error { return nil }
func (testConversations) Listen(context.Context, string, loopd.ActorRef, func(context.Context, string) ([]loopd.Message, error)) (loopd.ListenResult, error) {
	return loopd.ListenResult{}, nil
}
