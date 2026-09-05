package loopd

import "testing"

func TestActorIdentityAndRouting(t *testing.T) {
	for _, kind := range []ActorKind{ActorKindUser, ActorKindOperator, ActorKindHarness, "operator/longhorizon/manager"} {
		if !kind.Valid() {
			t.Fatalf("invalid kind %q", kind)
		}
	}
	custom := ActorRef{Kind: "operator/longhorizon/manager", Key: "run-uid"}
	if !custom.ValidTarget() || !custom.Kind.IsOperator() {
		t.Fatal("custom role must be a participating actor")
	}
	for _, kind := range []ActorKind{"", "manager", "operator//manager", "operator/longhorizon/", "operator/longhorizon/manager/extra"} {
		if kind.Valid() {
			t.Fatalf("accepted invalid kind %q", kind)
		}
	}
	if (ActorRef{Kind: ActorKindUser, Key: "human"}).ValidTarget() {
		t.Fatal("human is not an autonomous consumer")
	}
}
