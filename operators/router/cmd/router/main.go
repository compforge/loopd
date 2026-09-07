package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	operatorrouter "github.com/compforge/loopd/operators/router/internal/router"
	conversationv1alpha1 "github.com/compforge/loopd/pkg/k8s/v1alpha1"
	loopruntime "github.com/compforge/loopd/runtime"
	"github.com/go-logr/logr"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

const harnessTarget = "agentgo"

func main() {
	if err := run(); err != nil {
		slog.Error("Router Operator stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	logger := slog.Default()
	ctrl.SetLogger(logr.FromSlogHandler(logger.Handler()))
	runtime, err := loopruntime.New(envOr("LOOP_ROUTER_SERVER_URL", "http://127.0.0.1:8080"), loopruntime.Options{

		Logger: logger,
	})
	if err != nil {
		return err
	}
	defer runtime.Close()
	registerCtx, cancelRegister := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelRegister()
	if err := runtime.Loop.Operator.Register(registerCtx, loopruntime.OperatorRegistration{
		Key:         operatorrouter.OperatorKey,
		DisplayName: "Router",
		Description: "Routes a request to one or more temporary Harness calls and summarizes the result.",
	}); err != nil {
		return err
	}

	scheme := kruntime.NewScheme()
	if err := conversationv1alpha1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("register loopd Conversation scheme: %w", err)
	}
	namespace := envOr("LOOP_ROUTER_NAMESPACE", "default")
	manager, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Cache: cache.Options{DefaultNamespaces: map[string]cache.Config{
			namespace: {},
		}},
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
	})
	if err != nil {
		return fmt.Errorf("create Router manager: %w", err)
	}
	maxSubtasks, err := envInt("LOOP_ROUTER_MAX_SUBTASKS", 4)
	if err != nil {
		return err
	}
	reconciler, err := operatorrouter.New(runtime.Loop, operatorrouter.Config{
		HarnessTarget: envOr("LOOP_ROUTER_HARNESS_TARGET", harnessTarget), MaxSubtasks: maxSubtasks, Logger: logger,
	})
	if err != nil {
		return err
	}
	concurrency, err := envInt("LOOP_ROUTER_CONCURRENCY", 4)
	if err != nil {
		return err
	}
	if err := reconciler.SetupWithManager(manager, concurrency); err != nil {
		return fmt.Errorf("watch Router tasks: %w", err)
	}
	logger.Info("Router Operator starting",
		"operator_key", operatorrouter.OperatorKey,
		"namespace", namespace,
		"harness_target", harnessTarget,
		"max_subtasks", maxSubtasks,
		"concurrency", concurrency,
	)
	return manager.Start(ctrl.SetupSignalHandler())
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}
