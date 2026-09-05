package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOperatorAndHarnessRegistriesStartLeases(t *testing.T) {
	registered := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPut {
			http.NotFound(response, request)
			return
		}
		var input registrationRequest
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		if input.LeaseSeconds != 30 {
			t.Errorf("lease seconds = %d, want 30", input.LeaseSeconds)
		}
		registered <- request.URL.Path
		response.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	runtime, err := New(server.URL, Options{HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	if err := runtime.Loop.Operator.Register(context.Background(), OperatorRegistration{
		Key: "router", DisplayName: "Router",
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Loop.Harness.Register(context.Background(), HarnessRegistration{
		Key: "agentd", DisplayName: "Agentd",
	}); err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{<-registered: true, <-registered: true}
	if !paths["/v1/operators/router"] || !paths["/v1/harnesses/agentd"] {
		t.Fatalf("registration paths = %v", paths)
	}
}
