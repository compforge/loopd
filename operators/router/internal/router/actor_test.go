package router

import "testing"

func TestTemporaryHarnessIdentity(t *testing.T) {
	first := routerHarnessActor("input", "work/0")
	retry := routerHarnessActor("input", "work/0")
	parallel := routerHarnessActor("input", "work/1")
	if first.Kind != "operator/router/harness" || *first != *retry || first.Key == parallel.Key {
		t.Fatalf("first=%+v retry=%+v parallel=%+v", first, retry, parallel)
	}
}
