package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	hertzserver "github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/network/standard"
	"github.com/compforge/loopd/server"
)

func main() {
	if err := run(); err != nil {
		slog.Error("loop-server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	address := envOr("LOOP_SERVER_ADDR", ":8080")
	databasePath := envOr("LOOP_SERVER_DB", "loopd.db")
	loopServer, err := server.New(server.Config{
		Database: server.DatabaseConfig{Path: databasePath}, Logger: slog.Default(),
	})
	if err != nil {
		return err
	}
	defer loopServer.Close()

	logger := slog.Default()
	httpServer := hertzserver.Default(
		hertzserver.WithHostPorts(address),
		hertzserver.WithTransport(standard.NewTransporter),
		hertzserver.WithReadTimeout(30*time.Second),
		// A page may observe one task for hours, but its Operator or Harness
		// execution continues independently when the connection disappears.
		hertzserver.WithWriteTimeout(0),
		hertzserver.WithIdleTimeout(90*time.Second),
		hertzserver.WithMaxRequestBodySize(1<<20),
		hertzserver.WithSenseClientDisconnection(true),
	)
	loopServer.Register(httpServer.Engine)

	processCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go loopServer.Run(processCtx)
	serveErr := make(chan error, 1)
	go func() {
		logger.Info("loop-server listening", "address", address, "database", databasePath)
		serveErr <- httpServer.Run()
	}()
	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("serve loop-server HTTP: %w", err)
		}
		return nil
	case <-processCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
