// Package runtime provides the Go collaboration toolkit embedded by Operator reconcilers.
package runtime

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/runtime/infra"
	"github.com/compforge/loopd/runtime/service"
	"github.com/compforge/loopd/runtime/verb"
)

type Options struct {
	HTTPClient            *http.Client
	RequestTimeout        time.Duration
	RegistryLeaseDuration time.Duration
	Logger                *slog.Logger
}

type Runtime struct {
	Loop   Loop
	cancel context.CancelFunc
}

// Loop exposes collaboration Verbs. A Verb's effect is read (observe existing
// facts) or write (initiate work or change collaboration state). Identity and
// retry guarantees belong to each Verb, not to a generic persistent Effect engine.
type Loop struct {
	Conv     Conv
	Human    Human
	Harness  Harness
	Operator Operator
}

// New assembles the public verbs and their shared transport.
// +rule=`Dependency direction is verb → service → infra; shared model types never import the root runtime facade.`
func New(baseURL string, options Options) (*Runtime, error) {
	c, err := infra.NewClient(baseURL, infra.Options{HTTPClient: options.HTTPClient, RequestTimeout: options.RequestTimeout, Logger: options.Logger})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	loop := Loop{
		Conv:     verb.NewConv(service.NewConv(c)),
		Human:    verb.NewHuman(service.NewHuman(c)),
		Harness:  verb.NewHarness(service.NewHarness(c), service.NewRegistry(ctx, c, contract.ActorKindHarness, "harnesses", options.RegistryLeaseDuration, c.Logger())),
		Operator: verb.NewOperator(service.NewRegistry(ctx, c, contract.ActorKindOperator, "operators", options.RegistryLeaseDuration, c.Logger())),
	}
	return &Runtime{Loop: loop, cancel: cancel}, nil
}

// Close stops local registration heartbeats. Server-owned Harness runs continue.
func (runtime *Runtime) Close() error {
	if runtime.cancel != nil {
		runtime.cancel()
	}
	return nil
}
