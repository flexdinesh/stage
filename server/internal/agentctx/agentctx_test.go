package agentctx

import (
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
)

func TestParseSession(t *testing.T) {
	for in, want := range map[string]db.SessionRef{
		"3:abc":         {ConnectionID: 3, ExternalSessionID: "abc"},
		" 12:a:b/c ":    {ConnectionID: 12, ExternalSessionID: "a:b/c"},
		"7:opaque%2Fid": {ConnectionID: 7, ExternalSessionID: "opaque%2Fid"},
	} {
		got, err := ParseSession(in)
		if err != nil || got != want {
			t.Errorf("ParseSession(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "abc", "3", "3:", "3: ", "0:x", "-1:x", "x:y", ":y"} {
		if _, err := ParseSession(bad); err == nil {
			t.Errorf("ParseSession(%q) accepted", bad)
		}
	}
}

func TestCallerPrecedence(t *testing.T) {
	env := &db.SessionRef{ConnectionID: 1, ExternalSessionID: "env"}
	explicit := &db.SessionRef{ConnectionID: 2, ExternalSessionID: "explicit"}
	claim := &db.SessionRef{ConnectionID: 2, ExternalSessionID: "claim"}
	base := Caller{Session: env, Cwd: "/w"}

	if c, err := base.Resolve(nil, "", nil); err != nil || c.Session != env || c.Cwd != "/w" {
		t.Fatalf("environment: %+v %v", c, err)
	}
	if c, err := base.Resolve(explicit, "/other", nil); err != nil || c.Session != explicit || c.Cwd != "/other" {
		t.Fatalf("explicit overrides environment: %+v %v", c, err)
	}
	if c, err := base.Resolve(nil, "", claim); err != nil || c.Session != claim {
		t.Fatalf("claim overrides environment: %+v %v", c, err)
	}
	same := *claim
	if c, err := base.Resolve(&same, "", claim); err != nil || *c.Session != *claim {
		t.Fatalf("agreeing explicit and claim: %+v %v", c, err)
	}
	if _, err := base.Resolve(explicit, "", claim); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("conflict accepted: %v", err)
	}
	if _, err := base.Resolve(&db.SessionRef{ConnectionID: 0, ExternalSessionID: "x"}, "", nil); err == nil {
		t.Fatal("incomplete explicit caller accepted")
	}
}

func TestFromEnv(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == CallerEnv {
				return v
			}
			return ""
		}
	}
	if c := fromEnv(env("4:sess")); c.Session == nil || *c.Session != (db.SessionRef{ConnectionID: 4, ExternalSessionID: "sess"}) {
		t.Fatalf("set: %+v", c)
	}
	if c := fromEnv(env("")); c.Session != nil {
		t.Fatalf("unset: %+v", c)
	}
	bad := fromEnv(env("sess"))
	if bad.Session != nil {
		t.Fatalf("malformed named a caller: %+v", bad)
	}
	if _, err := bad.Resolve(nil, "", nil); err == nil || !strings.Contains(err.Error(), CallerEnv) {
		t.Fatalf("malformed environment not reported: %v", err)
	}
	explicit := &db.SessionRef{ConnectionID: 1, ExternalSessionID: "x"}
	if c, err := bad.Resolve(explicit, "", nil); err != nil || c.Session != explicit {
		t.Fatalf("explicit caller should bypass a malformed environment: %+v %v", c, err)
	}
}
