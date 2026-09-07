package api

import (
	"context"
	"errors"

	app "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	sse "github.com/cloudwego/hertz/pkg/protocol/sse"
	ui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/service"
)

func (s *Server) submitHarnessRun(ctx context.Context, c *app.RequestContext) error {
	if s.HarnessRuns == nil {
		return service.ErrUnavailable
	}
	var input contract.HarnessRunRequest
	if err := decodeBody(c, &input); err != nil {
		return err
	}
	run, err := s.HarnessRuns.Submit(ctx, input)
	if err != nil {
		return err
	}
	c.JSON(consts.StatusAccepted, run)
	return nil
}
func (s *Server) observeHarnessRun(ctx context.Context, c *app.RequestContext) error {
	if s.HarnessRuns == nil {
		return service.ErrUnavailable
	}
	result, err := s.HarnessRuns.Get(ctx, c.Param("run_id"))
	if err != nil {
		return err
	}
	c.JSON(consts.StatusOK, result)
	return nil
}
func (s *Server) cancelHarnessRun(ctx context.Context, c *app.RequestContext) error {
	if s.HarnessRuns == nil {
		return service.ErrUnavailable
	}
	if err := s.HarnessRuns.Cancel(ctx, c.Param("run_id")); err != nil {
		return err
	}
	c.Status(consts.StatusAccepted)
	return nil
}

func (s *Server) streamHarnessRun(ctx context.Context, c *app.RequestContext) error {
	if s.HarnessRuns == nil {
		return service.ErrUnavailable
	}
	var writer *sse.Writer
	err := s.HarnessRuns.Stream(ctx, c.Param("run_id"), func(event ui.Event) error {
		data, err := event.Marshal()
		if err != nil {
			return err
		}
		if writer == nil {
			writer = sse.NewWriter(c)
		}
		return writer.WriteEvent("", "", data)
	})
	if writer == nil {
		return err
	}
	closeErr := writer.Close()
	if !errors.Is(err, context.Canceled) && errors.Join(err, closeErr) != nil {
		s.logger.WarnContext(ctx, "Harness stream stopped", "run_id", c.Param("run_id"), "error", errors.Join(err, closeErr))
	}
	return nil
}
