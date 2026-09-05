package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultRegistryLeaseDuration = 30 * time.Second

type registry struct {
	kind          string
	path          string
	client        *client
	runCtx        context.Context
	leaseDuration time.Duration
	logger        *slog.Logger
}

type registration struct {
	key         string
	displayName string
	description string
}

func newRegistry(
	runCtx context.Context,
	client *client,
	kind string,
	path string,
	leaseDuration time.Duration,
	logger *slog.Logger,
) registry {
	if leaseDuration <= 0 {
		leaseDuration = defaultRegistryLeaseDuration
	}
	return registry{kind: kind, path: path, client: client, runCtx: runCtx, leaseDuration: leaseDuration, logger: logger}
}

func (service registry) register(ctx context.Context, value registration) error {
	value.key = strings.TrimSpace(value.key)
	value.displayName = strings.TrimSpace(value.displayName)
	value.description = strings.TrimSpace(value.description)
	if value.key == "" {
		return fmt.Errorf("registration key is required")
	}
	if err := service.renew(ctx, value); err != nil {
		return fmt.Errorf("register %s %q: %w", service.kind, value.key, err)
	}
	service.logger.InfoContext(ctx, service.kind+" registered",
		"key", value.key,
		"lease", service.leaseDuration,
	)
	go service.keepAlive(value)
	return nil
}

func (service registry) keepAlive(value registration) {
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
				service.logger.WarnContext(service.runCtx, "renew "+service.kind+" lease failed",
					"key", value.key,
					"error", err,
				)
			}
		}
	}
}

func (service registry) renew(ctx context.Context, value registration) error {
	path := fmt.Sprintf("/v1/%s/%s", service.path, url.PathEscape(value.key))
	return service.client.do(ctx, http.MethodPut, path, registrationRequest{
		DisplayName:  value.displayName,
		Description:  value.description,
		LeaseSeconds: int(service.leaseDuration / time.Second),
	}, nil)
}

type registrationRequest struct {
	DisplayName  string `json:"display_name,omitempty"`
	Description  string `json:"description,omitempty"`
	LeaseSeconds int    `json:"lease_seconds"`
}
