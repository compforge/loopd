package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	agent "github.com/compforge/agentgo"
	"github.com/compforge/agentgo/llm"
	"github.com/compforge/loopd/harness"
	agentgoharness "github.com/compforge/loopd/harness/agentgo"
	operatorrouter "github.com/compforge/loopd/operators/router/internal/router"
	loopruntime "github.com/compforge/loopd/runtime"
	taskv1alpha1 "github.com/compforge/loopd/runtime/api/v1alpha1"
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
	modelOptions := []llm.ModelOption{
		llm.WithRequestTimeout(30 * time.Minute),
		llm.WithStreamIdleTimeout(2 * time.Minute),
	}
	if value := os.Getenv("LOOP_ROUTER_API_KEY"); value != "" {
		modelOptions = append(modelOptions, llm.WithAPIKey(value))
	}
	if value := os.Getenv("LOOP_ROUTER_BASE_URL"); value != "" {
		modelOptions = append(modelOptions, llm.WithBaseURL(value))
	}
	model, err := llm.NewModel(
		envOr("LOOP_ROUTER_MODEL_PROVIDER", "openai"),
		envOr("LOOP_ROUTER_MODEL", "gpt-5-mini"),
		modelOptions...,
	)
	if err != nil {
		return fmt.Errorf("create Router model: %w", err)
	}
	adapter, err := agentgoharness.New(func(context.Context, harness.Request) (*agent.Agent, error) {
		return agent.NewAgent(agent.WithModel(model), agent.WithMaxTurns(8)), nil
	})
	if err != nil {
		return err
	}

	runtime, err := loopruntime.New(envOr("LOOP_ROUTER_SERVER_URL", "http://127.0.0.1:8080"), loopruntime.Options{
		Harnesses: map[string]harness.Adapter{harnessTarget: adapter},
		Logger:    logger,
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
	if err := taskv1alpha1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("register loopd Task scheme: %w", err)
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
		HarnessTarget: harnessTarget, MaxSubtasks: maxSubtasks, Logger: logger,
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
