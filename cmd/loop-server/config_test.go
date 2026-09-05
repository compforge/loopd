package main

import (
	"strings"
	"testing"
	"time"
)

func TestLoadConfigDefaults(t *testing.T) {
	clearConfigEnv(t)
	config, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.address != ":8080" || config.databaseDriver != "sqlite" || config.databaseDSN != "loopd.db" {
		t.Fatalf("config = %#v", config)
	}
	if config.redisAddress != "127.0.0.1:6379" || config.taskNamespace != "default" || config.taskClientTimeout != 10*time.Second {
		t.Fatalf("task config = %#v", config)
	}
}

func TestLoadConfigUsesUnprefixedEnvironment(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("SERVER_ADDRESS", ":9090")
	t.Setenv("DATABASE_DRIVER", "mysql")
	t.Setenv("DATABASE_DSN", "user:pass@tcp(mysql:3306)/loopd")
	t.Setenv("REDIS_ADDRESS", "redis:6379")
	t.Setenv("TASK_NAMESPACE", "loopd-system")
	t.Setenv("HTTP_IDLE_TIMEOUT", "2m")
	config, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.address != ":9090" || config.databaseDriver != "mysql" || config.databaseDSN == "" {
		t.Fatalf("config = %#v", config)
	}
	if config.redisAddress != "redis:6379" || config.taskNamespace != "loopd-system" || config.idleTimeout != 2*time.Minute {
		t.Fatalf("runtime config = %#v", config)
	}
}

func TestLoadConfigRejectsInvalidDuration(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("HTTP_READ_TIMEOUT", "never")
	if _, err := loadConfig(); err == nil {
		t.Fatal("loadConfig accepted an invalid HTTP_READ_TIMEOUT")
	}
}

func TestLoadConfigRequiresMySQLDSN(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DATABASE_DRIVER", "mysql")
	if _, err := loadConfig(); err == nil {
		t.Fatal("loadConfig accepted MySQL without DATABASE_DSN")
	}
}

func TestLoadConfigRejectsLegacyEnvironment(t *testing.T) {
	for _, test := range []struct{ name, replacement string }{
		{"LOOP_SERVER_MYSQL_DSN", "DATABASE_DRIVER=mysql and DATABASE_DSN"},
		{"LOOP_SERVER_SQLITE_PATH", "DATABASE_DRIVER=sqlite and DATABASE_DSN"},
		{"LOOP_SERVER_ADDR", "SERVER_ADDRESS"},
		{"LOOP_SERVER_REDIS_ADDR", "REDIS_ADDRESS"},
		{"LOOP_SERVER_REDIS_USERNAME", "REDIS_USERNAME"},
		{"LOOP_SERVER_REDIS_PASSWORD", "REDIS_PASSWORD"},
		{"LOOP_SERVER_TASK_NAMESPACE", "TASK_NAMESPACE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv(test.name, "private-config-value")
			_, err := loadConfig()
			if err == nil {
				t.Fatal("legacy configuration silently accepted")
			}
			if !strings.Contains(err.Error(), test.name) || !strings.Contains(err.Error(), test.replacement) {
				t.Fatalf("missing migration guidance: %v", err)
			}
			if strings.Contains(err.Error(), "private-config-value") {
				t.Fatal("configuration value leaked in error")
			}
		})
	}
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"SERVER_ADDRESS", "DATABASE_DRIVER", "DATABASE_DSN", "REDIS_ADDRESS", "REDIS_USERNAME", "REDIS_PASSWORD",
		"TASK_NAMESPACE", "TASK_CLIENT_TIMEOUT", "HTTP_READ_TIMEOUT", "HTTP_IDLE_TIMEOUT", "SHUTDOWN_TIMEOUT",
		"LOOP_SERVER_MYSQL_DSN", "LOOP_SERVER_SQLITE_PATH", "LOOP_SERVER_ADDR", "LOOP_SERVER_REDIS_ADDR",
		"LOOP_SERVER_REDIS_USERNAME", "LOOP_SERVER_REDIS_PASSWORD", "LOOP_SERVER_TASK_NAMESPACE",
	} {
		t.Setenv(name, "")
	}
}
