package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	loopd "github.com/compforge/loopd"
)

func TestActorRegisterStartsLease(t *testing.T) {
	registered := make(chan actorRegistrationRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPut || request.URL.Path != "/v1/actors/operator/router" {
			http.NotFound(response, request)
			return
		}
		var input actorRegistrationRequest
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		registered <- input
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"kind":"operator","key":"router"}`))
	}))
	t.Cleanup(server.Close)
	runtime, err := New(server.URL, Options{HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	err = runtime.Loop.Actor.Register(context.Background(), loopd.Actor{
		ActorRef:    loopd.ActorRef{Kind: loopd.RoleOperator, Key: "router"},
		DisplayName: "Router",
	})
	if err != nil {
		t.Fatal(err)
	}
	input := <-registered
	if input.DisplayName != "Router" || input.LeaseSeconds != 30 {
		t.Fatalf("registration = %#v", input)
	}
}
