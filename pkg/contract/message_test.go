package contract

import (
	"encoding/json"
	"testing"
)

func TestMessageSourceJSON(t *testing.T) {
	message := Message{SourceKind: ActorKindUser, SourceKey: "alice", TargetKind: ActorKindOperator, TargetKey: "router"}
	for name, value := range map[string]any{"message": message, "info": message.Info()} {
		t.Run(name, func(t *testing.T) {
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]string{"source_kind": `"user"`, "source_key": `"alice"`, "target_kind": `"operator"`, "target_key": `"router"`} {
				if string(fields[key]) != want {
					t.Fatalf("%s = %s, want %s", key, fields[key], want)
				}
			}
			for _, key := range []string{"kind", "key", "actor_key"} {
				if _, exists := fields[key]; exists {
					t.Fatalf("unexpected Message field %s", key)
				}
			}
			var decoded MessageInfo
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.SourceKind != message.SourceKind || decoded.SourceKey != message.SourceKey {
				t.Fatalf("source identity lost: %+v", decoded)
			}
		})
	}
}
