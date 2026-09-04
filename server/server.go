// Package server composes loop-server's HTTP, service, and persistence layers.
package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/cloudwego/hertz/pkg/route"
	loopd "github.com/compforge/loopd"
	serverapi "github.com/compforge/loopd/server/internal/api"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/compforge/loopd/server/internal/service"
)

type Config struct {
	Database   DatabaseConfig
	Responders []loopd.Responder
	Logger     *slog.Logger
}

type DatabaseConfig struct {
	Path             string
	OperationTimeout time.Duration
	MaxOpenConns     int
	MaxIdleConns     int
	ConnMaxLifetime  time.Duration
	ConnMaxIdleTime  time.Duration
}

type Server struct {
	store *repo.Store
	api   *serverapi.Server
}

func New(config Config) (*Server, error) {
	store, err := repo.Open(repo.Config{
		Path: config.Database.Path, OperationTimeout: config.Database.OperationTimeout,
		MaxOpenConns: config.Database.MaxOpenConns, MaxIdleConns: config.Database.MaxIdleConns,
		ConnMaxLifetime: config.Database.ConnMaxLifetime, ConnMaxIdleTime: config.Database.ConnMaxIdleTime,
	})
	if err != nil {
		return nil, err
	}
	loopService := service.New(store, config.Responders)
	return &Server{store: store, api: serverapi.New(loopService, config.Logger)}, nil
}

func (server *Server) Register(engine *route.Engine) { server.api.Register(engine) }
func (server *Server) Run(context.Context)           {}
func (server *Server) Close() error                  { return server.store.Close() }
