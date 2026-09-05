package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/compforge/loopd/operators/interaction/internal/interaction"
	rt "github.com/compforge/loopd/runtime"
	convapi "github.com/compforge/loopd/runtime/api/v1alpha1"
	"github.com/go-logr/logr"
	krt "k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	metrics "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func run() error {
	ctrl.SetLogger(logr.FromSlogHandler(slog.Default().Handler()))
	runtime, err := rt.New(envOr("SERVER_URL", "http://127.0.0.1:8080"), rt.Options{})
	if err != nil {
		return err
	}
	defer runtime.Close()
	scheme := krt.NewScheme()
	if err := convapi.AddToScheme(scheme); err != nil {
		return err
	}
	namespace := envOr("CONVERSATION_NAMESPACE", "default")
	manager, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:  scheme,
		Cache:   cache.Options{DefaultNamespaces: map[string]cache.Config{namespace: {}}},
		Metrics: metrics.Options{BindAddress: "0"}, HealthProbeBindAddress: "0",
	})
	if err != nil {
		return err
	}
	if err := interaction.New(runtime.Loop).SetupWithManager(manager); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := runtime.Loop.Operator.Register(ctx, rt.OperatorRegistration{
		Key: interaction.OperatorKey, DisplayName: "交互演示 · Ask → Confirm",
		Description: "先选择，再确认，最后汇总；每步等待 10 秒，支持取消和超时。",
	}); err != nil {
		return err
	}
	slog.Info("interaction operator registered", "operator_key", interaction.OperatorKey, "namespace", namespace)
	return manager.Start(ctrl.SetupSignalHandler())
}

func main() {
	if err := run(); err != nil {
		slog.Error("interaction operator stopped", "error", err)
		os.Exit(1)
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
