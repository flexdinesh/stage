package codex_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/codex"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func trustRequest(path string) execution.WorkspaceAddRequest {
	input, _ := json.Marshal(map[string]string{"path": path})
	return execution.WorkspaceAddRequest{Source: codex.SourceTrust, Input: input, RequestID: "req-1", Confirmed: true}
}

func invalidReason(t *testing.T, err error) string {
	t.Helper()
	var invalid *execution.Invalid
	if !errors.As(err, &invalid) {
		t.Fatalf("want *execution.Invalid, got %T %v", err, err)
	}
	return invalid.Reason
}

func TestWorkspaceAddDeclaresTheTrustSource(t *testing.T) {
	r := newRig(t)
	c, err := r.adapter.Features(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !c.Operations[execution.OpWorkspaceAdd].Available || c.DeclaresOperations() != nil || c.ValidateWorkspaceSources() != nil {
		t.Fatalf("declaration: %+v %v %v", c.Operations[execution.OpWorkspaceAdd], c.DeclaresOperations(), c.ValidateWorkspaceSources())
	}
	if len(c.WorkspaceSources) != 1 {
		t.Fatalf("sources %+v", c.WorkspaceSources)
	}
	s := c.WorkspaceSources[0]
	if s.ID != "trust" || s.Label != "Trust folder" || !s.Availability.Available || !strings.HasPrefix(s.Confirm, "Codex will trust this folder") ||
		len(s.Input) != 1 || s.Input[0].ID != "path" || s.Input[0].Kind != execution.FieldPath || !s.Input[0].Required {
		t.Fatalf("trust source %+v", s)
	}
	if _, ok := execution.As[execution.WorkspaceAdder](r.adapter); !ok {
		t.Fatal("adapter hides WorkspaceAdder")
	}
}

// Trusting writes through config/batchWrite and keeps every other project
// entry and every other field of the folder's entry.
func TestWorkspaceAddTrustsAFolderAndKeepsOtherProjects(t *testing.T) {
	r := newRig(t)
	dir := t.TempDir()
	other := t.TempDir()
	r.srv.Trust(other, "trusted")
	r.srv.SetProject(dir, map[string]any{"trust_level": "untrusted", "note": "keep"})
	got, err := r.adapter.AddWorkspace(t.Context(), trustRequest(dir+"/"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Existing || got.Workspace.ID != dir || got.Workspace.WorkspaceRoot != dir || !got.Workspace.Local ||
		got.Workspace.ImportIf != execution.ImportInCheckout || got.Message != "Codex trusts this folder now." || len(got.Facts) != 0 {
		t.Fatalf("added %+v", got)
	}
	if e := r.srv.Project(dir); e["trust_level"] != "trusted" || e["note"] != "keep" {
		t.Fatalf("folder entry %+v", e)
	}
	if e := r.srv.Project(other); e["trust_level"] != "trusted" {
		t.Fatalf("other entry %+v", e)
	}
	if r.srv.Calls("config/batchWrite") != 1 {
		t.Fatalf("writes %d", r.srv.Calls("config/batchWrite"))
	}
	ws, err := r.adapter.Workspaces(t.Context())
	if err != nil || !slices.ContainsFunc(ws, func(w execution.WorkspaceInfo) bool { return w.ID == dir }) {
		t.Fatalf("listed %+v %v", ws, err)
	}
}

func TestWorkspaceAddExistingTrustChangesNothing(t *testing.T) {
	r := newRig(t)
	dir := t.TempDir()
	r.srv.Trust(dir, "trusted")
	got, err := r.adapter.AddWorkspace(t.Context(), trustRequest(dir))
	if err != nil || !got.Existing || got.Workspace.ID != dir || got.Message != "Codex already trusts this folder." {
		t.Fatalf("existing %+v %v", got, err)
	}
	if r.srv.Calls("config/batchWrite") != 0 {
		t.Fatal("existing trust wrote config")
	}
}

// Dots, quotes and spaces stay inside one projects key: an unquoted key
// path would split on the dots and nest tables.
func TestWorkspaceAddDottedPathsStayOneKey(t *testing.T) {
	r := newRig(t)
	base := t.TempDir()
	for _, name := range []string{"a.b/c.d", "v1.2 final", `q"uo\te.x`} {
		dir := filepath.Join(base, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		got, err := r.adapter.AddWorkspace(t.Context(), trustRequest(dir))
		if err != nil || got.Existing || got.Workspace.ID != dir {
			t.Fatalf("%q: %+v %v", name, got, err)
		}
		if e := r.srv.Project(dir); e["trust_level"] != "trusted" {
			t.Fatalf("%q entry %+v", name, e)
		}
	}
	for _, p := range r.srv.ProjectPaths() {
		if e := r.srv.Project(p); e["trust_level"] != "trusted" || len(e) != 1 {
			t.Fatalf("stray project entry %q: %+v (keys %v)", p, e, r.srv.ProjectPaths())
		}
	}
	if len(r.srv.ProjectPaths()) != 3 {
		t.Fatalf("project keys %v", r.srv.ProjectPaths())
	}
}

func TestWorkspaceAddRefusesBadInputBeforeWriting(t *testing.T) {
	r := newRig(t)
	file := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	unconfirmed := trustRequest(t.TempDir())
	unconfirmed.Confirmed = false
	other := trustRequest(t.TempDir())
	other.Source = "clone"
	for name, tc := range map[string]struct {
		req  execution.WorkspaceAddRequest
		want string
	}{
		"not a directory": {trustRequest(file), "is not a folder on the Codex host"},
		"missing":         {trustRequest(filepath.Join(t.TempDir(), "gone")), "Codex cannot read"},
		"relative":        {trustRequest("rel/dir"), "absolute folder path"},
		"unconfirmed":     {unconfirmed, "Confirm before"},
		"unknown source":  {other, `no workspace source "clone"`},
	} {
		_, err := r.adapter.AddWorkspace(t.Context(), tc.req)
		if reason := invalidReason(t, err); !strings.Contains(reason, tc.want) {
			t.Fatalf("%s: %q", name, reason)
		}
	}
	if r.srv.Calls("config/batchWrite") != 0 || len(r.srv.ProjectPaths()) != 0 {
		t.Fatal("refused input wrote config")
	}
}

func TestWorkspaceAddReadonlyLayerIsInvalid(t *testing.T) {
	r := newRig(t)
	r.srv.ReadonlyConfig(true)
	_, err := r.adapter.AddWorkspace(t.Context(), trustRequest(t.TempDir()))
	if reason := invalidReason(t, err); reason != "Codex refused to trust the folder: Only writes to the user config are allowed" {
		t.Fatalf("readonly %q", reason)
	}
	if len(r.srv.ProjectPaths()) != 0 {
		t.Fatal("readonly config changed")
	}
}

// A version conflict re-reads and retries once with the new version; a
// second conflict gives up.
func TestWorkspaceAddRetriesOnceOnVersionConflict(t *testing.T) {
	r := newRig(t)
	dir := t.TempDir()
	r.srv.ConcurrentWrites(1)
	got, err := r.adapter.AddWorkspace(t.Context(), trustRequest(dir))
	if err != nil || got.Existing || r.srv.Project(dir)["trust_level"] != "trusted" || r.srv.Calls("config/batchWrite") != 2 || r.srv.Calls("config/read") < 3 {
		t.Fatalf("retry %+v %v writes=%d reads=%d", got, err, r.srv.Calls("config/batchWrite"), r.srv.Calls("config/read"))
	}

	again := t.TempDir()
	r.srv.ConcurrentWrites(2)
	if _, err := r.adapter.AddWorkspace(t.Context(), trustRequest(again)); err == nil || !strings.Contains(err.Error(), "kept changing") {
		t.Fatalf("second conflict: %v", err)
	}
	if r.srv.Project(again) != nil || r.srv.Calls("config/batchWrite") != 4 {
		t.Fatalf("gave up after %d writes, entry %+v", r.srv.Calls("config/batchWrite"), r.srv.Project(again))
	}
}

func TestWorkspaceAddOverriddenTrust(t *testing.T) {
	r := newRig(t)
	kept := t.TempDir()
	r.srv.Override(kept, "trusted", "Managed config sets this project's trust")
	got, err := r.adapter.AddWorkspace(t.Context(), trustRequest(kept))
	// The managed layer already trusts it: nothing to write.
	if err != nil || !got.Existing {
		t.Fatalf("managed trust %+v %v", got, err)
	}

	denied := t.TempDir()
	r.srv.Override(denied, "untrusted", "Managed config marks this project untrusted")
	_, err = r.adapter.AddWorkspace(t.Context(), trustRequest(denied))
	if reason := invalidReason(t, err); reason != "Codex saved the trust, but Managed config marks this project untrusted; Codex does not trust this folder." {
		t.Fatalf("overridden %q", reason)
	}
	if r.srv.Project(denied)["trust_level"] != "trusted" {
		t.Fatal("user layer write missing")
	}
}

// okOverridden while the folder ends up trusted is a success with a warn
// fact.
func TestWorkspaceAddOverriddenButTrustedWarns(t *testing.T) {
	r := newRig(t)
	dir := t.TempDir()
	r.srv.Override(dir, "", "A managed layer also sets this project")
	got, err := r.adapter.AddWorkspace(t.Context(), trustRequest(dir))
	if err != nil || got.Existing || got.Message != "Codex trusts this folder now." || len(got.Facts) != 1 {
		t.Fatalf("okOverridden %+v %v", got, err)
	}
	if f := got.Facts[0]; f.Tone != execution.ToneWarn || f.Value != "A managed layer also sets this project" {
		t.Fatalf("fact %+v", f)
	}
}

func TestWorkspaceAddUnsupportedMethodNamesTheUpdate(t *testing.T) {
	r := newRig(t)
	r.srv.Unsupported("fs/getMetadata")
	_, err := r.adapter.AddWorkspace(t.Context(), trustRequest(t.TempDir()))
	if !errors.Is(err, execution.ErrUnavailable) || !strings.Contains(err.Error(), "Update Codex") {
		t.Fatalf("unsupported %v", err)
	}
}

// A lost write reply is an uncertain outcome; the retry finds the folder
// trusted.
func TestWorkspaceAddLostWriteReplyIsUncertain(t *testing.T) {
	r := newRig(t)
	dir := t.TempDir()
	r.srv.DropReply("config/batchWrite", 1)
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	_, err := r.adapter.AddWorkspace(ctx, trustRequest(dir))
	var uncertain *execution.OutcomeUncertain
	if !errors.As(err, &uncertain) {
		t.Fatalf("lost reply %T %v", err, err)
	}
	got, err := r.adapter.AddWorkspace(t.Context(), trustRequest(dir))
	if err != nil || !got.Existing {
		t.Fatalf("retry %+v %v", got, err)
	}
}
