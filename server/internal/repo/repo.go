// Package repo owns loop-server persistence.
package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/compforge/loopd/server/internal/migrations"
	"github.com/compforge/loopd/server/internal/model"
	drivermysql "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const defaultOperationTimeout = 10 * time.Second

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

type Config struct {
	Driver           string
	DSN              string
	OperationTimeout time.Duration
	MaxOpenConns     int
	MaxIdleConns     int
	ConnMaxLifetime  time.Duration
	ConnMaxIdleTime  time.Duration
}

type Store struct {
	db               *gorm.DB
	operationTimeout time.Duration
}

func Open(config Config) (*Store, error) {
	if config.OperationTimeout <= 0 {
		config.OperationTimeout = defaultOperationTimeout
	}
	if config.ConnMaxLifetime <= 0 {
		config.ConnMaxLifetime = 30 * time.Minute
	}
	if config.ConnMaxIdleTime <= 0 {
		config.ConnMaxIdleTime = 5 * time.Minute
	}

	dialector, backend, err := databaseDialector(config)
	if err != nil {
		return nil, err
	}
	if config.MaxOpenConns <= 0 {
		config.MaxOpenConns = 32
	}
	if config.MaxIdleConns <= 0 {
		config.MaxIdleConns = 8
	}
	if backend == "sqlite" {
		// SQLite is the single-process Quick Start backend. One connection
		// serializes transactions and avoids avoidable database-locked errors.
		config.MaxOpenConns = 1
		config.MaxIdleConns = 1
	}
	if config.MaxIdleConns > config.MaxOpenConns {
		return nil, errors.New("database max idle connections exceeds max open connections")
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("open loopd %s database: %w", backend, err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("access loopd database pool: %w", err)
	}
	sqlDB.SetMaxOpenConns(config.MaxOpenConns)
	sqlDB.SetMaxIdleConns(config.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(config.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(config.ConnMaxIdleTime)

	store := &Store{db: db, operationTimeout: config.OperationTimeout}
	ctx, cancel := store.withTimeout(context.Background())
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping loopd %s database: %w", backend, err)
	}
	// Schema inspection spans many round trips, especially over a development
	// tunnel. Give startup its own budget without relaxing request timeouts.
	migrationCtx, cancelMigration := context.WithTimeout(context.Background(), time.Minute)
	defer cancelMigration()
	// Rename existing columns before AutoMigrate can add empty replacements.
	if err := migrations.DomainKeys(db.WithContext(migrationCtx)); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("migrate loopd domain keys: %w", err)
	}
	if err := db.WithContext(migrationCtx).AutoMigrate(
		&model.Conversation{},
		&model.Message{},
		&model.Operator{},
		&model.Harness{},
	); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("migrate loopd database: %w", err)
	}
	if err := migrations.ConversationOwnership(db.WithContext(migrationCtx)); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("migrate conversation ownership: %w", err)
	}
	return store, nil
}

func databaseDialector(config Config) (gorm.Dialector, string, error) {
	driver := strings.ToLower(strings.TrimSpace(config.Driver))
	if driver == "" {
		driver = "sqlite"
	}
	switch driver {
	case "sqlite":
		if config.DSN == "" {
			config.DSN = "loopd.db"
		}
		return gormsqlite.Open(config.DSN), driver, nil
	case "mysql":
		dsn, err := drivermysql.ParseDSN(config.DSN)
		if err != nil {
			return nil, "", fmt.Errorf("parse loopd MySQL DSN: %w", err)
		}
		dsn.ParseTime = true
		dsn.Loc = time.UTC
		dsn.Timeout = config.OperationTimeout
		dsn.ReadTimeout = config.OperationTimeout
		dsn.WriteTimeout = config.OperationTimeout
		if dsn.Params == nil {
			dsn.Params = map[string]string{}
		}
		if dsn.Params["charset"] == "" {
			dsn.Params["charset"] = "utf8mb4"
		}
		return gormmysql.Open(dsn.FormatDSN()), driver, nil
	default:
		return nil, "", fmt.Errorf("unsupported database driver %q", config.Driver)
	}
}

func (store *Store) Close() error {
	db, err := store.db.DB()
	if err != nil {
		return err
	}
	return db.Close()
}

func (store *Store) withTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, store.operationTimeout)
}

func mapError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrConflict
	}
	return err
}
