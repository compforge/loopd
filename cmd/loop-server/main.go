package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	hertzserver "github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/network/standard"
	loopd "github.com/compforge/loopd"
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
	responders, err := loadResponders(os.Getenv("LOOP_SERVER_RESPONDERS"))
	if err != nil {
		return err
	}
	store, err := server.OpenStore(server.StoreConfig{Path: databasePath})
	if err != nil {
		return err
	}
	defer store.Close()

	logger := slog.Default()
	loopServer := server.New(store, server.Config{Responders: responders, Logger: logger})
	httpServer := hertzserver.Default(
		hertzserver.WithHostPorts(address),
		hertzserver.WithTransport(standard.NewTransporter),
		hertzserver.WithReadTimeout(30*time.Second),
		// A page may observe an Invocation for hours, but the durable Invocation
		// continues independently when this SSE connection disappears.
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

func loadResponders(raw string) ([]loopd.Responder, error) {
	if raw == "" {
		return nil, nil
	}
	var responders []loopd.Responder
	if err := json.Unmarshal([]byte(raw), &responders); err != nil {
		return nil, fmt.Errorf("decode LOOP_SERVER_RESPONDERS: %w", err)
	}
	for _, responder := range responders {
		if !responder.ResponderRef.Valid() {
			return nil, fmt.Errorf("invalid responder %q/%q", responder.Role, responder.ID)
		}
	}
	return responders, nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
