package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	loopd "github.com/compforge/loopd"
	loopruntime "github.com/compforge/loopd/runtime"
)

// +case=`Router 用 Read 分页选取输入之前的历史；只保留最近 100 条，不消费后来输入`
func TestReadHistorySelectsBoundedTailBeforeInput(t *testing.T) {
	var afters []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/conversations/conv/messages" || r.URL.Query().Get("limit") != "100" {
			t.Errorf("unexpected history request: %s %s", r.Method, r.URL)
		}
		after := r.URL.Query().Get("after")
		afters = append(afters, after)
		var page []loopd.Message
		for i := 1; i <= 260 && len(page) < 100; i++ {
			id := fmt.Sprintf("m%04d", i)
			if id > after {
				page = append(page, loopd.Message{ID: id})
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
	if len(afters) != 3 || afters[0] != "" || afters[1] != "m0100" || afters[2] != "m0200" {
		t.Fatalf("pagination=%v", afters)
	}
	history, err = reconciler.readHistory(context.Background(), "conv", "m0001")
	if err != nil || len(history) != 0 {
		t.Fatalf("first input has history=%+v err=%v", history, err)
	}
}
