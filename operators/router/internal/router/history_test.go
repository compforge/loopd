package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/compforge/loopd/pkg/contract"
	loopruntime "github.com/compforge/loopd/runtime"
)

// +case=`Router 用 Read 分页选取输入之前的历史；只保留最近 100 条，不消费后来输入`
func TestReadHistorySelectsBoundedTailBeforeInput(t *testing.T) {
	var afters []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/content") {
			_ = json.NewEncoder(w).Encode(contract.Message{ID: strings.Split(r.URL.Path, "/")[5]})
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/v1/conversations/conv/messages" || r.URL.Query().Get("limit") != "100" {
			t.Errorf("unexpected history request: %s %s", r.Method, r.URL)
		}
		after := r.URL.Query().Get("before")
		if r.URL.Query().Get("order") != "desc" {
			t.Error("history must query newest first")
		}
		afters = append(afters, after)
		var page []contract.Message
		for i := 260; i >= 1 && len(page) < 100; i-- {
			id := fmt.Sprintf("m%04d", i)
			if id < after {
				page = append(page, contract.Message{ID: id})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": page})
	}))
	defer server.Close()
	runtime, err := loopruntime.New(server.URL, loopruntime.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	reconciler := &Reconciler{loop: runtime.Loop}
	history, err := reconciler.readHistory(context.Background(), "conv", "m0251")
	if err != nil || len(history) != 100 || history[0].ID != "m0151" || history[99].ID != "m0250" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	if len(afters) != 1 || afters[0] != "m0251" {
		t.Fatalf("pagination=%v", afters)
	}
	history, err = reconciler.readHistory(context.Background(), "conv", "m0001")
	if err != nil || len(history) != 0 {
		t.Fatalf("first input has history=%+v err=%v", history, err)
	}
}
