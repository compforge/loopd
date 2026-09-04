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
	taskv1alpha1 "github.com/compforge/loopd/runtime/api/v1alpha1"
	"github.com/compforge/loopd/server"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	kubeconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
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
	taskNamespace := envOr("LOOP_SERVER_TASK_NAMESPACE", "default")
	tasks, err := newTaskClient(taskNamespace)
	if err != nil {
		return err
	}
	loopServer, err := server.New(server.Config{
		Database: server.DatabaseConfig{Path: databasePath}, Tasks: tasks, Logger: slog.Default(),
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
		logger.Info("loop-server listening",
			"address", address,
			"database", databasePath,
			"task_namespace", taskNamespace,
		)
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

func newTaskClient(namespace string) (server.TaskClient, error) {
	scheme := runtime.NewScheme()
	if err := taskv1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register loopd Task scheme: %w", err)
	}
	config, err := kubeconfig.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("load Kubernetes config: %w", err)
	}
	kubeClient, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes client: %w", err)
	}
	return server.NewKubernetesTaskClient(kubeClient, namespace, 10*time.Second), nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
