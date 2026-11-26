package api_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/codex"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/engine"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/opencode"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// testHarnesses is the server composition the API tests run against.
var testHarnesses = harness.MustRegistry(t3.Descriptor, opencode.Descriptor)

// hostedFake is a harness that runs as local processes.
var hostedFake = harness.Descriptor{Kind: "hosted-fake", Name: "Hosted fake", Sessions: harness.SessionsMachine, LocalProcesses: true}

type harnessesReply struct {
	Machine struct {
		ID      int64           `json:"id"`
		Label   string          `json:"label"`
		Profile harness.Profile `json:"profile"`
	} `json:"machine"`
	Harnesses []struct {
		Kind           string            `json:"kind"`
		Name           string            `json:"name"`
		LocalProcesses bool              `json:"localProcesses"`
		Sessions       harness.Sessions  `json:"sessions"`
		StateReasons   []string          `json:"stateReasons"`
		Available      bool              `json:"available"`
		Reason         string            `json:"reason"`
		Closures       []harness.Closure `json:"closures"`
		LinkSchemes    []string          `json:"linkSchemes"`
		DiscoveryHint  *string           `json:"discoveryHint"`
	} `json:"harnesses"`
}

func TestHarnessesListDescriptorsAndMachineAvailability(t *testing.T) {
	for _, tc := range []struct {
		name    string
		profile harness.Profile
		hosted  bool
	}{
		{"desktop", harness.ResolveProfile(true, false, func(string) string { return "" }), true},
		{"foreground", harness.ResolveProfile(false, false, func(string) string { return "" }), false},
		{"foreground opt-in", harness.ResolveProfile(false, true, func(string) string { return "" }), true},
		{"container", harness.ResolveProfile(true, true, func(k string) string {
			if k == harness.ProfileEnv {
				return harness.ProfileContainer
			}
			return ""
		}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, func(d *api.Deps) {
				d.Harnesses = harness.MustRegistry(t3.Descriptor, opencode.Descriptor, hostedFake)
				// The local machine hosts with the case's profile.
				d.Engine = engine.NewForMachines(d.Store, nil, machine.MustSet(machine.NewLocal(db.LocalMachine, tc.profile, nil)), nil)
			})
			var out harnessesReply
			if code := e.do(http.MethodGet, "/api/harnesses", nil, &out); code != http.StatusOK {
				t.Fatalf("status %d", code)
			}
			if out.Machine.ID != 1 || out.Machine.Profile != tc.profile || len(out.Harnesses) != 3 {
				t.Fatalf("reply %+v", out)
			}
			t3h, oc, hosted := out.Harnesses[0], out.Harnesses[1], out.Harnesses[2]
			if t3h.Kind != "t3" || t3h.Name != "T3" || t3h.LocalProcesses || t3h.Sessions != harness.SessionsMachine || !t3h.Available || len(t3h.Closures) != 1 || t3h.Closures[0].Via != "via T3 settle" {
				t.Fatalf("t3 %+v", t3h)
			}
			if oc.Kind != "opencode" || oc.LocalProcesses || oc.Sessions != harness.SessionsMachine || !oc.Available || oc.Closures[0].Signal != "archived" {
				t.Fatalf("opencode %+v", oc)
			}
			if !slices.Equal(t3h.LinkSchemes, []string{"t3code"}) || oc.LinkSchemes == nil || len(oc.LinkSchemes) != 0 {
				t.Fatalf("link schemes: t3 %v, opencode %v", t3h.LinkSchemes, oc.LinkSchemes)
			}
			if oc.StateReasons == nil || len(oc.StateReasons) != 0 || t3h.StateReasons == nil || len(t3h.StateReasons) != 0 {
				t.Fatalf("state reasons: t3 %#v, opencode %#v", t3h.StateReasons, oc.StateReasons)
			}
			if t3h.DiscoveryHint == nil || !strings.Contains(*t3h.DiscoveryHint, "as a project in T3") ||
				oc.DiscoveryHint == nil || !strings.Contains(*oc.DiscoveryHint, "Open the workspace in OpenCode") || hosted.DiscoveryHint != nil {
				t.Fatalf("discovery hints: t3 %+v, opencode %+v, hosted %+v", t3h, oc, hosted)
			}
			if !hosted.LocalProcesses || hosted.Sessions != harness.SessionsMachine || hosted.Available != tc.hosted || (hosted.Reason == "") != tc.hosted || hosted.Closures == nil {
				t.Fatalf("hosted %+v", hosted)
			}
		})
	}
}

// mustConnectors validates m, the local machine's connectors, against
// testHarnesses.
func mustConnectors(m map[string]harness.Connector) harness.Connectors {
	c, err := harness.NewConnectors(testHarnesses, map[int64]map[string]harness.Connector{db.LocalMachine: m})
	if err != nil {
		panic(err)
	}
	return c
}

func TestHarnessConnectionRoutes(t *testing.T) {
	e := newEnv(t, func(d *api.Deps) {
		d.Harnesses = harness.MustRegistry(t3.Descriptor, opencode.Descriptor, hostedFake)
	})
	var list struct {
		Harnesses []struct {
			Kind        string        `json:"kind"`
			Connectable bool          `json:"connectable"`
			Setup       harness.Setup `json:"setup"`
			Attribution struct {
				MetaKeys []string `json:"metaKeys"`
				Env      string   `json:"env"`
			} `json:"attribution"`
		} `json:"harnesses"`
	}
	e.must("GET", "/api/harnesses", nil, 200, &list)
	t3h, oc, hosted := list.Harnesses[0], list.Harnesses[1], list.Harnesses[2]
	if !t3h.Connectable || len(t3h.Setup.Fields) != 3 || t3h.Setup.Fields[0].ID != "url" || t3h.Setup.Fields[1].ID != "publicUrl" || t3h.Setup.Fields[2].ID != "adminToken" || len(t3h.Setup.Actions) != 2 || !t3h.Setup.Disconnect {
		t.Fatalf("t3 setup %+v", t3h)
	}
	// newEnv composes no OpenCode connector; a hosted harness has none.
	if oc.Connectable || hosted.Connectable || hosted.Setup.Fields == nil || hosted.Setup.Actions == nil {
		t.Fatalf("not connectable %+v %+v", oc, hosted)
	}
	// Attribution is the descriptor's, never null; the hint stays server-side.
	if t3h.Attribution.MetaKeys == nil || len(t3h.Attribution.MetaKeys) != 0 || t3h.Attribution.Env != "T3CODE_THREAD_ID" ||
		len(oc.Attribution.MetaKeys) != 1 || oc.Attribution.MetaKeys[0] != "ai.opencode/sessionID" {
		t.Fatalf("attribution %+v %+v", t3h.Attribution, oc.Attribution)
	}

	var st struct {
		Kind         string `json:"kind"`
		Name         string `json:"name"`
		ConnectionID int64  `json:"connectionId"`
		Configured   bool   `json:"configured"`
		Connected    bool   `json:"connected"`
		State        string `json:"state"`
		Fields       []harness.FieldState
		Actions      []harness.ActionState
		Details      map[string]any `json:"details"`
	}
	e.must("GET", "/api/harnesses/t3/status", nil, 200, &st)
	if st.Kind != "t3" || st.Name != "T3" || st.ConnectionID != e.store.ActiveID(db.LocalMachine, "t3") || !st.Connected || !st.Configured || st.State != "connected" ||
		len(st.Fields) != 3 || st.Fields[0].ID != "url" || st.Fields[0].Source != "default" || st.Fields[2].ID != "adminToken" || st.Fields[2].Set ||
		len(st.Actions) != 2 || st.Actions[1].ID != "auto-pair" || st.Actions[1].Available || !strings.Contains(st.Actions[1].Reason, "fake T3") || st.Details["publicUrl"] != "http://127.0.0.1:3773" {
		t.Fatalf("status %+v", st)
	}

	e.must("GET", "/api/harnesses/nope/status", nil, 404, nil)
	e.must("GET", "/api/harnesses/hosted-fake/status", nil, 409, nil)
	e.must("POST", "/api/harnesses/hosted-fake/actions/pair", nil, 409, nil)
	e.must("POST", "/api/harnesses/t3/actions/nope", nil, 404, nil)
	e.must("PUT", "/api/harnesses/t3/settings", map[string]any{"nope": 1}, 400, nil)
	e.must("POST", "/api/harnesses/t3/actions/auto-pair", map[string]any{"extra": true}, 400, nil)
	e.must("PUT", "/api/harnesses/t3/settings", map[string]any{"url": "http://localhost:4000"}, 200, &st)
	if st.Fields[0].Value != "http://localhost:4000" || st.Fields[0].Source != "saved" || !st.Fields[0].Set {
		t.Fatalf("saved url %+v", st.Fields)
	}

	var h api.Health
	e.must("GET", "/api/health", nil, 200, &h)
	if len(h.Harnesses) != 1 || h.Harnesses[0].Kind != "t3" || h.Harnesses[0].ConnectionID != e.store.ActiveID(db.LocalMachine, "t3") {
		t.Fatalf("health harnesses %+v", h.Harnesses)
	}
}

// Claude Code's descriptor reaches clients with its attribution precedence
// and origin, and T3's with what it wraps (the CLI ranks harness session
// variables by them), and a connect action.
func TestHarnessListServesClaudeCode(t *testing.T) {
	e := newEnv(t, func(d *api.Deps) {
		d.Harnesses = harness.MustRegistry(t3.Descriptor, opencode.Descriptor, claudecode.Descriptor, codex.Descriptor)
	})
	var list struct {
		Harnesses []struct {
			Kind           string           `json:"kind"`
			LocalProcesses bool             `json:"localProcesses"`
			Sessions       harness.Sessions `json:"sessions"`
			StateReasons   []string         `json:"stateReasons"`
			Launch         string           `json:"launch"`
			SessionHint    *string          `json:"sessionHint"`
			DiscoveryHint  string           `json:"discoveryHint"`
			Setup          harness.Setup    `json:"setup"`
			Attribution    struct {
				Env        string              `json:"env"`
				Precedence *harness.Precedence `json:"precedence"`
				Origin     *harness.Origin     `json:"origin"`
			} `json:"attribution"`
			Wraps *harness.Wraps `json:"wraps"`
		} `json:"harnesses"`
	}
	e.must("GET", "/api/harnesses", nil, 200, &list)
	cc, cx := list.Harnesses[2], list.Harnesses[3]
	if !strings.Contains(cc.DiscoveryHint, "Run Claude Code in the workspace") || !strings.Contains(cx.DiscoveryHint, "Trust the workspace folder in Codex") {
		t.Fatalf("discovery hints: Claude Code %q, Codex %q", cc.DiscoveryHint, cx.DiscoveryHint)
	}
	// Claude Code names how its sessions are pasted; T3 leaves the field out.
	if cc.SessionHint == nil || !strings.Contains(*cc.SessionHint, "claude agents") || list.Harnesses[0].SessionHint != nil {
		t.Fatalf("sessionHint claude %v t3 %v", cc.SessionHint, list.Harnesses[0].SessionHint)
	}
	p := cc.Attribution.Precedence
	if cc.Kind != claudecode.Descriptor.Kind || !cc.LocalProcesses || cc.Sessions != harness.SessionsMachine ||
		!slices.Equal(cc.StateReasons, []string{execution.ReasonOwnerExited}) || cc.Launch != "correlated" || cc.Attribution.Env != "CLAUDE_CODE_SESSION_ID" || p == nil ||
		len(p.Owns) != 1 || p.Owns[0] != (harness.EnvMatch{Env: "CLAUDE_CODE_SESSION_KIND", Value: "bg"}) || len(p.Yields) != 0 ||
		cc.Attribution.Origin == nil || cc.Attribution.Origin.Env != "CLAUDE_CODE_ENTRYPOINT" || cc.Wraps != nil {
		t.Fatalf("%+v %+v", cc, p)
	}
	if w := list.Harnesses[0].Wraps; w == nil || !slices.Equal(w.Origins, []string{t3.OriginEntrypoint, t3.OriginOriginator}) || list.Harnesses[0].Attribution.Origin != nil {
		t.Fatalf("T3 wraps %+v", w)
	}
	if _, ok := cc.Setup.Action(claudecode.ActionConnect); !ok || !cc.Setup.Disconnect || len(cc.Setup.Fields) != 1 {
		t.Fatalf("setup %+v", cc.Setup)
	}
	if list.Harnesses[0].Attribution.Precedence != nil {
		t.Fatal("T3 declares no precedence")
	}
}
