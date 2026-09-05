package runtime

import "context"

type Operator struct {
	registry registry
}

type OperatorRegistration struct {
	Key         string
	DisplayName string
	Description string
}

func (service Operator) Register(ctx context.Context, value OperatorRegistration) error {
	return service.registry.register(ctx, registration{
		key: value.Key, displayName: value.DisplayName, description: value.Description,
	})
}
