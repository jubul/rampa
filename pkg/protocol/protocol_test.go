package protocol

import (
	"encoding/json"
	"testing"
)

func TestEnvelopeRoundTrip(t *testing.T) {
	cmd := Command{ID: "c1", Kind: CommandDeploy, Deploy: &DeployCommand{
		DeploymentID: "d1", App: "x", Scope: "dev", Namespace: "x-dev",
		Labels:  map[string]string{"rampa.dev/app": "x"},
		Objects: []json.RawMessage{json.RawMessage(`{"kind":"Service"}`)},
	}}
	env, err := NewEnvelope(KindCommand, "m1", "c1", cmd)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(env)
	var back Envelope
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	var got Command
	if err := json.Unmarshal(back.Payload, &got); err != nil {
		t.Fatal(err)
	}
	if got.Kind != CommandDeploy || got.Deploy == nil || got.Deploy.Namespace != "x-dev" || len(got.Deploy.Objects) != 1 {
		t.Fatalf("round trip roto: %+v", got)
	}
}
