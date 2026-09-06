package contract

import (
	"strings"
	"testing"
)

func TestActorIdentityAndRouting(t *testing.T) {
	for _, kind := range []ActorKind{ActorKindUser, ActorKindOperator, ActorKindHarness, "operator/longhorizon/manager", "operator/planner", "operator/longhorizon/manager/delegate", "harness/local", "user/customer", "future-kind"} {
		if !kind.Valid() {
			t.Fatalf("invalid kind %q", kind)
		}
	}
	custom := ActorRef{Kind: "operator/longhorizon/manager", Key: "run-uid"}
	if !custom.ValidTarget() || !custom.Kind.IsOperator() {
		t.Fatal("custom role must be a participating actor")
	}
	for _, kind := range []ActorKind{"", " ", " operator", "harness ", ActorKind(strings.Repeat("x", 129))} {
		if kind.Valid() {
			t.Fatalf("accepted invalid kind %q", kind)
		}
	}
	if (ActorRef{Kind: ActorKindUser, Key: "human"}).ValidTarget() {
		t.Fatal("human is not an autonomous consumer")
	}
}
