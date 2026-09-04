package service

import (
	"context"
	"log/slog"
	"strings"
	"time"

	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/qiankunli/go-stdx/uuid"
)

const (
	defaultActorLease = 30 * time.Second
	maxActorLease     = 5 * time.Minute
)

type ActorRepository interface {
	RegisterOperator(context.Context, model.Operator) (model.Operator, error)
	RegisterHarness(context.Context, model.Harness) (model.Harness, error)
	ListOperators(context.Context, time.Time) ([]model.Operator, error)
	ListHarnesses(context.Context, time.Time) ([]model.Harness, error)
}

type ActorService struct {
	repo   ActorRepository
	logger *slog.Logger
}

func NewActorService(repository ActorRepository, logger *slog.Logger) *ActorService {
	return &ActorService{repo: repository, logger: loggerOrDefault(logger)}
}

func (service *ActorService) Register(
	ctx context.Context,
	actor loopd.Actor,
	lease time.Duration,
) (loopd.Actor, error) {
	actor.Key = strings.TrimSpace(actor.Key)
	actor.DisplayName = strings.TrimSpace(actor.DisplayName)
	actor.Description = strings.TrimSpace(actor.Description)
	if !actor.ActorRef.ValidTarget() || lease < 0 || lease > maxActorLease {
		return loopd.Actor{}, ErrInvalid
	}
	if lease == 0 {
		lease = defaultActorLease
	}
	expiresAt := time.Now().UTC().Add(lease)
	var err error
	switch actor.Kind {
	case loopd.RoleOperator:
		var registered model.Operator
		registered, err = service.repo.RegisterOperator(ctx, model.Operator{
			ID: uuid.V7(), Key: actor.Key, DisplayName: actor.DisplayName,
			Description: actor.Description, ExpiresAt: expiresAt,
		})
		actor = operatorFromModel(registered)
	case loopd.RoleHarness:
		var registered model.Harness
		registered, err = service.repo.RegisterHarness(ctx, model.Harness{
			ID: uuid.V7(), Key: actor.Key, DisplayName: actor.DisplayName,
			Description: actor.Description, ExpiresAt: expiresAt,
		})
		actor = harnessFromModel(registered)
	}
	if err != nil {
		return loopd.Actor{}, err
	}
	service.logger.DebugContext(ctx, "actor lease renewed",
		"kind", actor.Kind,
		"key", actor.Key,
		"expires_at", expiresAt,
	)
	return actor, nil
}

func (service *ActorService) List(ctx context.Context) ([]loopd.Actor, error) {
	now := time.Now().UTC()
	operators, err := service.repo.ListOperators(ctx, now)
	if err != nil {
		return nil, err
	}
	harnesses, err := service.repo.ListHarnesses(ctx, now)
	if err != nil {
		return nil, err
	}
	actors := make([]loopd.Actor, 0, len(operators)+len(harnesses))
	for _, operator := range operators {
		actors = append(actors, operatorFromModel(operator))
	}
	for _, harness := range harnesses {
		actors = append(actors, harnessFromModel(harness))
	}
	return actors, nil
}

func operatorFromModel(value model.Operator) loopd.Actor {
	return loopd.Actor{
		ActorRef:    loopd.ActorRef{Kind: loopd.RoleOperator, Key: value.Key},
		DisplayName: value.DisplayName,
		Description: value.Description,
	}
}

func harnessFromModel(value model.Harness) loopd.Actor {
	return loopd.Actor{
		ActorRef:    loopd.ActorRef{Kind: loopd.RoleHarness, Key: value.Key},
		DisplayName: value.DisplayName,
		Description: value.Description,
	}
}
