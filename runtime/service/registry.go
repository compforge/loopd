package service

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/runtime/infra"
	"github.com/compforge/loopd/runtime/model"
)

const defaultRegistryLeaseDuration = 30 * time.Second

type Registry struct {
	kind          contract.ActorKind
	path          string
	client        *infra.Client
	runCtx        context.Context
	leaseDuration time.Duration
	logger        *slog.Logger
}

func NewRegistry(
	runCtx context.Context,
	client *infra.Client,
	kind contract.ActorKind,
	path string,
	leaseDuration time.Duration,
	logger *slog.Logger,
) Registry {
	if leaseDuration <= 0 {
		leaseDuration = defaultRegistryLeaseDuration
	}
	return Registry{kind: kind, path: path, client: client, runCtx: runCtx, leaseDuration: leaseDuration, logger: logger}
}

func (service Registry) Register(ctx context.Context, value model.Registration) error {
	value.Key = strings.TrimSpace(value.Key)
	value.DisplayName = strings.TrimSpace(value.DisplayName)
	value.Description = strings.TrimSpace(value.Description)
	if value.Key == "" {
		return model.ErrRegistrationKeyRequired
	}
	if err := service.renew(ctx, value); err != nil {
		return fmt.Errorf("register %s %q: %w", service.kind, value.Key, err)
	}
	service.logger.InfoContext(ctx, string(service.kind)+" registered",
		"key", value.Key,
		"lease", service.leaseDuration,
	)
	go service.keepAlive(value)
	return nil
}

func (service Registry) keepAlive(value model.Registration) {
	interval := service.leaseDuration / 3
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-service.runCtx.Done():
			return
		case <-ticker.C:
			if err := service.renew(service.runCtx, value); err != nil && service.runCtx.Err() == nil {
				service.logger.WarnContext(service.runCtx, "renew "+string(service.kind)+" lease failed",
					"key", value.Key,
					"error", err,
				)
			}
		}
	}
}

func (service Registry) renew(ctx context.Context, value model.Registration) error {
	path := fmt.Sprintf("/v1/%s/%s", service.path, url.PathEscape(value.Key))
	return service.client.Do(ctx, http.MethodPut, path, registrationRequest{
		DisplayName:  value.DisplayName,
		Description:  value.Description,
		LeaseSeconds: int(service.leaseDuration / time.Second),
	}, nil)
}

type registrationRequest struct {
	DisplayName  string `json:"display_name,omitempty"`
	Description  string `json:"description,omitempty"`
	LeaseSeconds int    `json:"lease_seconds"`
}
