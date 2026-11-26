package store

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestAcceptedEvidenceReadsLegacyKeys(t *testing.T) {
	var e AcceptedEvidence
	if err := json.Unmarshal([]byte(`{"threadId":"old","threadLink":"https://example.com/session","executionState":"settled","artifacts":[{"label":"old"}],"historicalSettledAt":"2026-11-01T00:00:00Z"}`), &e); err != nil {
		t.Fatal(err)
	}
	if e.ExternalSessionID == nil || *e.ExternalSessionID != "old" || e.SessionLink == nil || *e.SessionLink != "https://example.com/session" || e.ExecutionState != StateDone {
		t.Fatalf("legacy evidence: %+v", e)
	}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"threadId", "threadLink", "artifacts", "historicalSettledAt"} {
		if _, ok := fields[key]; ok {
			t.Errorf("legacy key emitted: %s", key)
		}
	}
	if string(fields["externalSessionId"]) != `"old"` || string(fields["sessionLink"]) != `"https://example.com/session"` || string(fields["executionState"]) != `"done"` {
		t.Fatalf("new evidence: %s", raw)
	}
	var roundTrip AcceptedEvidence
	if err := json.Unmarshal(raw, &roundTrip); err != nil || !reflect.DeepEqual(roundTrip, e) {
		t.Fatalf("round trip: %+v %v", roundTrip, err)
	}

	if err := json.Unmarshal([]byte(`{"threadId":"old","externalSessionId":"new","threadLink":"old","sessionLink":null}`), &e); err != nil {
		t.Fatal(err)
	}
	if e.ExternalSessionID == nil || *e.ExternalSessionID != "new" || e.SessionLink != nil {
		t.Fatalf("new keys should take precedence, including null: %+v", e)
	}
}
