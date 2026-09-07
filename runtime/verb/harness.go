package verb

import (
	"context"

	"github.com/compforge/loopd/runtime/model"
	"github.com/compforge/loopd/runtime/service"
)

type Harness struct {
	service  service.Harness
	registry service.Registry
}

func NewHarness(s service.Harness, r service.Registry) Harness {
	return Harness{service: s, registry: r}
}

// Register advertises availability and renews the registration until Runtime.Close.
func (h Harness) Register(ctx context.Context, r model.HarnessRegistration) error {
	return h.registry.Register(ctx, r)
}

// Prompt submits a durable call; closing Runtime never ends remote execution.
func (h Harness) Prompt(ctx context.Context, p model.Prompt) (*service.Call, error) {
	return h.service.Prompt(ctx, p)
}

// Call restores a handle from a persisted Run ID without starting work.
func (h Harness) Call(id string) *service.Call { return h.service.Call(id) }
