package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	lhapi "github.com/compforge/loopd/operators/longhorizon/api/v1alpha1"
	lh "github.com/compforge/loopd/operators/longhorizon/internal/longhorizon"
	convapi "github.com/compforge/loopd/pkg/k8s/v1alpha1"
	loopruntime "github.com/compforge/loopd/runtime"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	metrics "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func main() {
	if err := run(); err != nil {
		slog.Error("LongHorizon stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	logger := slog.Default()
	ctrl.SetLogger(logr.FromSlogHandler(logger.Handler()))
	rounds, err := strconv.Atoi(env("LOOP_LH_MAX_ROUNDS", "25"))
	if err != nil || rounds < 1 || rounds > 1000 {
		return fmt.Errorf("LOOP_LH_MAX_ROUNDS must be between 1 and 1000")
	}
	config := lh.Config{MaxRounds: int32(rounds)}
	for name, dest := range map[string]*time.Duration{"LOOP_LH_RUN_TIMEOUT": &config.RunTimeout, "LOOP_LH_RETENTION_TTL": &config.RetentionTTL, "LOOP_LH_MANAGER_TIMEOUT": &config.ManagerTimeout, "LOOP_LH_EXECUTOR_TIMEOUT": &config.ExecutorTimeout, "LOOP_LH_AUDITOR_TIMEOUT": &config.AuditorTimeout, "LOOP_LH_HUMAN_TIMEOUT": &config.HumanTimeout} {
		if raw := os.Getenv(name); raw != "" {
			value, err := time.ParseDuration(raw)
			if err != nil || value <= 0 {
				return fmt.Errorf("%s must be a positive duration", name)
			}
			*dest = value
		}
	}
	loop, err := loopruntime.New(env("LOOP_LH_SERVER_URL", "http://127.0.0.1:8080"), loopruntime.Options{Logger: logger})
	if err != nil {
		return err
	}
	defer loop.Close()
	scheme := runtime.NewScheme()
	if err := convapi.AddToScheme(scheme); err != nil {
		return err
	}
	if err := lhapi.AddToScheme(scheme); err != nil {
		return err
	}
	namespace := env("LOOP_LH_NAMESPACE", "default")
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{Scheme: scheme, Cache: cache.Options{DefaultNamespaces: map[string]cache.Config{namespace: {}}}, Metrics: metrics.Options{BindAddress: "0"}, LeaderElection: true, LeaderElectionID: "longhorizon.loopd.compforge.io", LeaderElectionNamespace: namespace})
	if err != nil {
		return err
	}
	if err := lh.Setup(mgr, loop.Loop, config); err != nil {
		return err
	}
	ctx := ctrl.SetupSignalHandler()
	registerCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := loop.Loop.Operator.Register(registerCtx, loopruntime.OperatorRegistration{Key: lh.OperatorKey, DisplayName: "LongHorizon", Description: "Plans, executes and independently audits a CLI task; asks for human input when needed."}); err != nil {
		return err
	}
	return mgr.Start(ctx)
}
func env(name, fallback string) string {
	if s := os.Getenv(name); s != "" {
		return s
	}
	return fallback
}
