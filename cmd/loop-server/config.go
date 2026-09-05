package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type config struct {
	address           string
	databaseDriver    string
	databaseDSN       string
	redisAddress      string
	redisUsername     string
	redisPassword     string
	taskNamespace     string
	taskClientTimeout time.Duration
	readTimeout       time.Duration
	idleTimeout       time.Duration
	shutdownTimeout   time.Duration
}

func loadConfig() (config, error) {
	// An ignored store setting could silently redirect writes to the default SQLite file.
	for _, legacy := range []struct{ name, replacement string }{
		{"LOOP_SERVER_MYSQL_DSN", "DATABASE_DRIVER=mysql and DATABASE_DSN"},
		{"LOOP_SERVER_SQLITE_PATH", "DATABASE_DRIVER=sqlite and DATABASE_DSN"},
		{"LOOP_SERVER_ADDR", "SERVER_ADDRESS"},
		{"LOOP_SERVER_REDIS_ADDR", "REDIS_ADDRESS"},
		{"LOOP_SERVER_REDIS_USERNAME", "REDIS_USERNAME"},
		{"LOOP_SERVER_REDIS_PASSWORD", "REDIS_PASSWORD"},
		{"LOOP_SERVER_TASK_NAMESPACE", "TASK_NAMESPACE"},
	} {
		if os.Getenv(legacy.name) != "" {
			return config{}, fmt.Errorf("%s is no longer supported; remove it and configure %s", legacy.name, legacy.replacement)
		}
	}
	databaseDriver := strings.ToLower(strings.TrimSpace(envOr("DATABASE_DRIVER", "sqlite")))
	databaseDSN := os.Getenv("DATABASE_DSN")
	switch databaseDriver {
	case "sqlite":
		if databaseDSN == "" {
			databaseDSN = "loopd.db"
		}
	case "mysql":
		if databaseDSN == "" {
			return config{}, fmt.Errorf("DATABASE_DSN is required when DATABASE_DRIVER is %q", databaseDriver)
		}
	default:
		return config{}, fmt.Errorf("unsupported DATABASE_DRIVER %q", databaseDriver)
	}
	value := config{
		address:           envOr("SERVER_ADDRESS", ":8080"),
		databaseDriver:    databaseDriver,
		databaseDSN:       databaseDSN,
		redisAddress:      envOr("REDIS_ADDRESS", "127.0.0.1:6379"),
		redisUsername:     os.Getenv("REDIS_USERNAME"),
		redisPassword:     os.Getenv("REDIS_PASSWORD"),
		taskNamespace:     envOr("TASK_NAMESPACE", "default"),
		taskClientTimeout: 10 * time.Second,
		readTimeout:       30 * time.Second,
		idleTimeout:       90 * time.Second,
		shutdownTimeout:   15 * time.Second,
	}
	durations := []struct {
		name  string
		value *time.Duration
	}{
		{"TASK_CLIENT_TIMEOUT", &value.taskClientTimeout},
		{"HTTP_READ_TIMEOUT", &value.readTimeout},
		{"HTTP_IDLE_TIMEOUT", &value.idleTimeout},
		{"SHUTDOWN_TIMEOUT", &value.shutdownTimeout},
	}
	for _, item := range durations {
		parsed, err := durationEnv(item.name, *item.value)
		if err != nil {
			return config{}, err
		}
		*item.value = parsed
	}
	return value, nil
}

func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return value, nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
