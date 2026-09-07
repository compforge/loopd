package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	ui "github.com/compforge/agentue/sdks/go/ui"

	hertzapp "github.com/cloudwego/hertz/pkg/app"

	"github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/pkg/harness"
	"github.com/compforge/loopd/server/internal/component"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/compforge/loopd/server/internal/service"
)

type unstartedHarness struct{ t *testing.T }

func (h unstartedHarness) Prompt(context.Context, harness.Request) (harness.Call, error) {
	h.t.Fatal("HTTP submission or pre-start cancellation executed the Harness")
	return nil, nil
}

func TestHarnessHTTPAcceptsDurablyAndCancelSurvivesServiceRestart(t *testing.T) {
	store, err := repo.Open(repo.Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "loopd.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: "conv", ActorKind: contract.ActorKindOperator, ActorKey: "op"}); err != nil {
		t.Fatal(err)
	}
	adapters := map[string]harness.Adapter{"demo": unstartedHarness{t}}
	server := New(nil, nil, nil, nil, nil)
	server.HarnessRuns = service.NewHarnessRunService(store, adapters, nil)
	engine := route.NewEngine(config.NewOptions(nil))
	server.Register(engine)
	body := `{"conversation_id":"conv","idempotency_key":"once","effect_key":"work","target":"demo","text":"hello"}`
	accepted := performJSON(t, engine, "POST", "/v1/harness/runs", body)
	if accepted.StatusCode() != 202 {
		t.Fatalf("submit=%d %s", accepted.StatusCode(), accepted.Body())
	}
	var call contract.HarnessCall
	if err := json.Unmarshal(accepted.Body(), &call); err != nil {
		t.Fatal(err)
	}
	if call.ID == "" || call.MessageID == "" || call.Phase != contract.CallPending || call.DeadlineAt.IsZero() {
		t.Fatalf("accepted=%+v", call)
	}
	again := performJSON(t, engine, "POST", "/v1/harness/runs", body)
	var duplicate contract.HarnessCall
	if err := json.Unmarshal(again.Body(), &duplicate); err != nil {
		t.Fatal(err)
	}
	if again.StatusCode() != 202 || duplicate.ID != call.ID || !duplicate.DeadlineAt.Equal(call.DeadlineAt) {
		t.Fatalf("retry=%s", again.Body())
	}
	changed := performJSON(t, engine, "POST", "/v1/harness/runs", strings.Replace(body, "hello", "different", 1))
	if changed.StatusCode() != 409 {
		t.Fatalf("changed=%d %s", changed.StatusCode(), changed.Body())
	}
	invalid := performJSON(t, engine, "POST", "/v1/harness/runs", strings.Replace(body, `"demo"`, `"missing"`, 1))
	if invalid.StatusCode() != 400 {
		t.Fatalf("invalid=%d %s", invalid.StatusCode(), invalid.Body())
	}
	canceled := performJSON(t, engine, "POST", "/v1/harness/runs/"+call.ID+"/cancel", `{}`)
	if canceled.StatusCode() != 202 {
		t.Fatalf("cancel=%d %s", canceled.StatusCode(), canceled.Body())
	}
	runner := component.NewHarnessRunner(store, adapters, nil)
	if err := runner.Drive(ctx, call.ID); err != nil {
		t.Fatal(err)
	}
	server.HarnessRuns = service.NewHarnessRunService(store, adapters, nil)
	// Terminal snapshot replay does not need Redis or the process that drove the run.
	server.HarnessRuns.Listen = func(ctx context.Context, id string, deliver func(ui.Event) error) error {
		return component.NewMessageListener(nil, store, id, nil).Run(ctx, deliver)
	}
	observed := performJSON(t, engine, "GET", "/v1/harness/runs/"+call.ID, "")
	var final contract.HarnessCall
	if err := json.Unmarshal(observed.Body(), &final); err != nil {
		t.Fatal(err)
	}
	if observed.StatusCode() != 200 || final.Phase != contract.CallCancelled || final.Result != nil {
		t.Fatalf("final=%s", observed.Body())
	}

	// The Hertz test request needs a streaming writer, as in the Conv/Chat tests.
	request := hertzapp.NewContext(1)
	request.Request.SetMethod("GET")
	request.Request.SetRequestURI("/v1/harness/runs/" + call.ID + "/stream")
	writer := &streamWriter{}
	request.Response.HijackWriter(writer)
	engine.ServeHTTP(ctx, request)
	if request.Response.StatusCode() != 200 || !strings.Contains(writer.String(), `"op":"start"`) || !strings.Contains(writer.String(), `"op":"end"`) {
		t.Fatalf("stream=%d %s", request.Response.StatusCode(), writer.String())
	}

	missing := performJSON(t, engine, "GET", "/v1/harness/runs/missing", "")
	if missing.StatusCode() != 404 {
		t.Fatalf("missing=%d", missing.StatusCode())
	}
}
