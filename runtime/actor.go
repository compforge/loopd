package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	loopd "github.com/compforge/loopd"
)

const defaultActorLeaseDuration = 30 * time.Second

// Actor registers an Operator or directly addressable Harness with loop-server.
// The registration is a lease: the Actor disappears from discovery after this
// runtime stops renewing it.
type Actor struct {
	client        *client
	runCtx        context.Context
	leaseDuration time.Duration
	logger        *slog.Logger
}

func newActor(runCtx context.Context, client *client, leaseDuration time.Duration, logger *slog.Logger) Actor {
	if leaseDuration <= 0 {
		leaseDuration = defaultActorLeaseDuration
	}
	return Actor{client: client, runCtx: runCtx, leaseDuration: leaseDuration, logger: logger}
}

func (service Actor) Register(ctx context.Context, actor loopd.Actor) error {
	if !actor.ActorRef.ValidTarget() {
		return fmt.Errorf("invalid Actor %q/%q", actor.Kind, actor.Key)
	}
	if err := service.renew(ctx, actor); err != nil {
		return fmt.Errorf("register Actor %q/%q: %w", actor.Kind, actor.Key, err)
	}
	service.logger.InfoContext(ctx, "actor registered",
		"kind", actor.Kind,
		"key", actor.Key,
		"lease", service.leaseDuration,
	)
	go service.keepAlive(actor)
	return nil
}

func (service Actor) keepAlive(actor loopd.Actor) {
	ticker := time.NewTicker(service.leaseDuration / 3)
	defer ticker.Stop()
	for {
		select {
		case <-service.runCtx.Done():
			return
		case <-ticker.C:
			if err := service.renew(service.runCtx, actor); err != nil && service.runCtx.Err() == nil {
				service.logger.WarnContext(service.runCtx, "renew actor lease failed",
					"kind", actor.Kind,
					"key", actor.Key,
					"error", err,
				)
			}
		}
	}
}

func (service Actor) renew(ctx context.Context, actor loopd.Actor) error {
	path := fmt.Sprintf("/v1/actors/%s/%s",
		url.PathEscape(string(actor.Kind)),
		url.PathEscape(actor.Key),
	)
	return service.client.do(ctx, http.MethodPut, path, actorRegistrationRequest{
		DisplayName:  actor.DisplayName,
		Description:  actor.Description,
		LeaseSeconds: int(service.leaseDuration / time.Second),
	}, nil)
}

type actorRegistrationRequest struct {
	DisplayName  string `json:"display_name,omitempty"`
	Description  string `json:"description,omitempty"`
	LeaseSeconds int    `json:"lease_seconds"`
}
