package infra

import (
	"bufio"
	"context"
	"io"
	"strings"

	ui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/runtime/model"
)

// ReadEvents decodes AgentUE SSE until end or disconnect. An end frame closes
// this delivery only; the caller must still check durable execution state.
func ReadEvents(ctx context.Context, body io.Reader, events chan<- ui.Event) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), 32<<20)
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			continue
		}
		if line != "" || data.Len() == 0 {
			continue
		}
		event, err := ui.Parse([]byte(data.String()))
		data.Reset()
		if err != nil {
			return model.WrapError(err)
		}
		select {
		case events <- event:
		case <-ctx.Done():
			return model.WrapError(ctx.Err())
		}
		if event.Op == ui.OpEnd {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return TransportError(err)
	} else {
		return TransportError(io.ErrUnexpectedEOF)
	}
}
