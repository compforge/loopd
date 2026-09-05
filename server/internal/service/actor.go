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
	defaultRegistryLease = 30 * time.Second
	maxRegistryLease     = 5 * time.Minute
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

func (service *ActorService) RegisterOperator(
	ctx context.Context,
	key string,
	displayName string,
	description string,
	lease time.Duration,
) (loopd.Actor, error) {
	key, displayName, description, expiresAt, err := registration(key, displayName, description, lease)
	if err != nil {
		return loopd.Actor{}, err
	}
	registered, err := service.repo.RegisterOperator(ctx, model.Operator{
		ID: uuid.V7(), OperatorKey: key, DisplayName: displayName,
		Description: description, ExpiresAt: expiresAt,
	})
	if err != nil {
		return loopd.Actor{}, err
	}
	service.logRenewal(ctx, loopd.ActorKindOperator, registered.OperatorKey, expiresAt)
	return operatorFromModel(registered), nil
}

func (service *ActorService) RegisterHarness(
	ctx context.Context,
	key string,
	displayName string,
	description string,
	lease time.Duration,
) (loopd.Actor, error) {
	key, displayName, description, expiresAt, err := registration(key, displayName, description, lease)
	if err != nil {
		return loopd.Actor{}, err
	}
	registered, err := service.repo.RegisterHarness(ctx, model.Harness{
		ID: uuid.V7(), HarnessKey: key, DisplayName: displayName,
		Description: description, ExpiresAt: expiresAt,
	})
	if err != nil {
		return loopd.Actor{}, err
	}
	service.logRenewal(ctx, loopd.ActorKindHarness, registered.HarnessKey, expiresAt)
	return harnessFromModel(registered), nil
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

func (service *ActorService) logRenewal(ctx context.Context, kind loopd.ActorKind, key string, expiresAt time.Time) {
	service.logger.DebugContext(ctx, "registry lease renewed", "kind", kind, "key", key, "expires_at", expiresAt)
}

func registration(key, displayName, description string, lease time.Duration) (string, string, string, time.Time, error) {
	key = strings.TrimSpace(key)
	if key == "" || lease < 0 || lease > maxRegistryLease {
		return "", "", "", time.Time{}, ErrInvalid
	}
	if lease == 0 {
		lease = defaultRegistryLease
	}
	return key, strings.TrimSpace(displayName), strings.TrimSpace(description), time.Now().UTC().Add(lease), nil
}

func operatorFromModel(value model.Operator) loopd.Actor {
	return loopd.Actor{
		ActorRef:    loopd.ActorRef{Kind: loopd.ActorKindOperator, Key: value.OperatorKey},
		DisplayName: value.DisplayName,
		Description: value.Description,
	}
}

func harnessFromModel(value model.Harness) loopd.Actor {
	return loopd.Actor{
		ActorRef:    loopd.ActorRef{Kind: loopd.ActorKindHarness, Key: value.HarnessKey},
		DisplayName: value.DisplayName,
		Description: value.Description,
	}
}
