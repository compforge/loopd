// Package repo owns loop-server persistence.
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const defaultOperationTimeout = 10 * time.Second

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

type Config struct {
	Path             string
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
	if config.Path == "" {
		config.Path = "loopd.db"
	}
	if config.OperationTimeout <= 0 {
		config.OperationTimeout = defaultOperationTimeout
	}
	if config.MaxOpenConns <= 0 {
		config.MaxOpenConns = 1
	}
	if config.MaxIdleConns <= 0 {
		config.MaxIdleConns = 1
	}
	if config.ConnMaxLifetime <= 0 {
		config.ConnMaxLifetime = 30 * time.Minute
	}
	if config.ConnMaxIdleTime <= 0 {
		config.ConnMaxIdleTime = 5 * time.Minute
	}

	db, err := gorm.Open(sqlite.Open(config.Path), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("open loopd database: %w", err)
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
	if err := db.WithContext(ctx).AutoMigrate(&model.Conversation{}, &model.Message{}); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("migrate loopd database: %w", err)
	}
	return store, nil
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
