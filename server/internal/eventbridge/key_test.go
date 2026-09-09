package eventbridge

import "testing"

func TestMessageKey(t *testing.T) {
	const id = "01991af4-d832-7000-8000-000000000001"
	if got := MessageKey(id); got != "message/"+id {
		t.Fatalf("MessageKey = %q", got)
	}
}
