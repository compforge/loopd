// Package server composes loop-server's HTTP, service, and persistence layers.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	agentuerunner "github.com/compforge/agentue/sdks/go/runner"
	serverapi "github.com/compforge/loopd/server/internal/api"
	"github.com/compforge/loopd/server/internal/delivery"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/compforge/loopd/server/internal/service"
	"github.com/redis/go-redis/v9"
)

// HumanIdentity resolves the trusted user principal for chat creation and replies.
type HumanIdentity func(context.Context, *hertzapp.RequestContext) (string, error)

type Config struct {
	Conversations ConversationCoordinator
	Database      DatabaseConfig
	Redis         RedisConfig
	Logger        *slog.Logger
	HumanIdentity HumanIdentity
}

type DatabaseConfig struct {
	Driver           string
	DSN              string
	OperationTimeout time.Duration
	MaxOpenConns     int
	MaxIdleConns     int
	ConnMaxLifetime  time.Duration
	ConnMaxIdleTime  time.Duration
}

type Server struct {
	poll  *service.PollService
	store *repo.Store
	redis redis.UniversalClient
	api   *serverapi.Server
	human *service.HumanService
	chat  *service.ChatService
}

func New(config Config) (*Server, error) {
	if config.Conversations == nil {
		return nil, errors.New("conversation coordinator is required")
	}
	store, err := repo.Open(repo.Config{
		Driver: config.Database.Driver, DSN: config.Database.DSN,
		OperationTimeout: config.Database.OperationTimeout,
		MaxOpenConns:     config.Database.MaxOpenConns, MaxIdleConns: config.Database.MaxIdleConns,
		ConnMaxLifetime: config.Database.ConnMaxLifetime, ConnMaxIdleTime: config.Database.ConnMaxIdleTime,
	})
	if err != nil {
		return nil, err
	}
	events, redisClient, err := newEventBridge(config.Redis)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	conversations := service.NewConversationService(store, config.Logger)
	actors := service.NewActorService(store, config.Logger)
	messages := service.NewMessageService(store, config.Logger)
	chatDelivery := delivery.New(events, store, config.Logger)
	poll := service.NewPollService(store, config.Conversations, config.Logger)
	chat := service.NewChatService(store, chatDelivery, config.Logger, poll)
	contexts := service.NewContextService(store, config.Logger)
	human := service.NewHumanService(store, config.Logger)
	api := serverapi.New(actors, conversations, messages, chat, contexts, config.Logger)
	api.Human = human
	api.Poll = poll
	api.HumanIdentity = serverapi.HumanIdentity(config.HumanIdentity)
	return &Server{
		poll:  poll,
		human: human, chat: chat,
		store: store,
		redis: redisClient,
		api:   api,
	}, nil
}

func (server *Server) Register(engine *route.Engine) { server.api.Register(engine) }
func (server *Server) Run(ctx context.Context) {
	var workers sync.WaitGroup
	workers.Go(func() { server.human.Run(ctx) })
	workers.Go(func() { server.chat.Run(ctx) })
	workers.Go(func() { server.poll.Run(ctx) })
	workers.Wait()
}
func (server *Server) Close() error {
	return errors.Join(server.redis.Close(), server.store.Close())
}

func newEventBridge(config RedisConfig) (agentuerunner.EventBridge, redis.UniversalClient, error) {
	if config.Address == "" {
		config.Address = "127.0.0.1:6379"
	}
	if config.DialTimeout <= 0 {
		config.DialTimeout = 5 * time.Second
	}
	if config.ReadTimeout <= 0 {
		config.ReadTimeout = 5 * time.Second
	}
	if config.WriteTimeout <= 0 {
		config.WriteTimeout = 5 * time.Second
	}
	if config.PoolSize <= 0 {
		config.PoolSize = 20
	}
	if config.MinIdleConns < 0 {
		config.MinIdleConns = 0
	}
	if config.TaskTTL <= 0 {
		config.TaskTTL = 30 * 24 * time.Hour
	}
	if config.ReadBlock <= 0 {
		config.ReadBlock = time.Second
	}
	if config.ReadCount <= 0 {
		config.ReadCount = 100
	}
	if config.KeyPrefix == "" {
		config.KeyPrefix = "loopd:agentue"
	}
	client := redis.NewClient(&redis.Options{
		Addr: config.Address, Username: config.Username, Password: config.Password, DB: config.DB,
		DialTimeout: config.DialTimeout, ReadTimeout: config.ReadTimeout, WriteTimeout: config.WriteTimeout,
		PoolSize: config.PoolSize, MinIdleConns: config.MinIdleConns,
	})
	ctx, cancel := context.WithTimeout(context.Background(), config.DialTimeout)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("connect to loop-server Redis %q: %w", config.Address, err)
	}
	return agentuerunner.NewRedisEventBridge(client, agentuerunner.BridgeOptions{
		KeyPrefix: config.KeyPrefix, TaskTTL: config.TaskTTL,
		ReadBlock: config.ReadBlock, ReadCount: config.ReadCount,
	}), client, nil
}
