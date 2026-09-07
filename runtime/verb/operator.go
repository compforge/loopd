package verb

import (
	"context"

	"github.com/compforge/loopd/runtime/model"
	"github.com/compforge/loopd/runtime/service"
)

type Operator struct{ registry service.Registry }

func NewOperator(r service.Registry) Operator { return Operator{registry: r} }

// Register advertises availability and renews the registration until Runtime.Close.
func (o Operator) Register(ctx context.Context, r model.OperatorRegistration) error {
	return o.registry.Register(ctx, r)
}
