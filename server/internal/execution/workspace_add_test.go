package execution_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/execution"
)

var folderSource = execution.WorkspaceSource{
	ID: "folder", Label: "Folder", Availability: execution.Availability{Available: true},
	Input: []execution.Field{{ID: "path", Label: "Path", Kind: execution.FieldPath, Required: true}},
}

func addCaps(op execution.Availability, sources ...execution.WorkspaceSource) execution.Capabilities {
	return execution.Capabilities{Operations: map[execution.Op]execution.Availability{execution.OpWorkspaceAdd: op}, WorkspaceSources: sources}
}

func TestValidateWorkspaceSources(t *testing.T) {
	yes := execution.Availability{Available: true}
	unavailable := folderSource
	unavailable.ID, unavailable.Availability = "later", execution.Availability{Reason: "Sign in first."}
	for name, tc := range map[string]struct {
		caps execution.Capabilities
		want string
	}{
		"valid":                 {addCaps(yes, folderSource, unavailable), ""},
		"unavailable op, none":  {addCaps(execution.Availability{Reason: "Not yet."}), ""},
		"available op, none":    {addCaps(yes), "without an available workspace source"},
		"only unavailable":      {addCaps(yes, unavailable), "without an available workspace source"},
		"duplicate id":          {addCaps(yes, folderSource, folderSource), "unique stable id"},
		"unstable id":           {addCaps(yes, execution.WorkspaceSource{ID: "a b", Label: "A", Availability: yes}), "unique stable id"},
		"missing label":         {addCaps(yes, execution.WorkspaceSource{ID: "a", Label: " ", Availability: yes}), "unique stable id"},
		"unavailable no reason": {addCaps(yes, folderSource, execution.WorkspaceSource{ID: "b", Label: "B"}), "explain why"},
		"bad field": {addCaps(yes, execution.WorkspaceSource{ID: "a", Label: "A", Availability: yes,
			Input: []execution.Field{{ID: "p", Label: "P", Kind: execution.FieldPath, Choices: []execution.ActionChoice{{ID: "x", Label: "X"}}}}}), "does not accept choices"},
	} {
		t.Run(name, func(t *testing.T) {
			err := tc.caps.ValidateWorkspaceSources()
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("err %v, want %q", err, tc.want)
			}
		})
	}
}

func TestWorkspaceSourceInput(t *testing.T) {
	for input, want := range map[string]string{
		`{"path":"/home/u/repo"}`: "",
		`{"path":"relative"}`:     "", // absoluteness is the adapter's check
		`{}`:                      `Workspace source field "path" is required.`,
		`{"path":""}`:             `Invalid value for workspace source field "path".`,
		`{"path":7}`:              `Invalid value for workspace source field "path".`,
		`{"other":"x"}`:           "Workspace source input contains an unknown field.",
		`[]`:                      "Workspace source input must be a JSON object.",
	} {
		err := folderSource.ValidateInput(json.RawMessage(input))
		var invalid *execution.Invalid
		if want == "" && err != nil || want != "" && (!errors.As(err, &invalid) || invalid.Reason != want) {
			t.Errorf("%s: err %v, want %q", input, err, want)
		}
	}
	// Action errors keep their wording.
	err := execution.Action{Input: folderSource.Input}.ValidateInput(json.RawMessage(`{}`))
	if err == nil || err.Error() != `Action field "path" is required.` {
		t.Fatalf("action err %v", err)
	}
}

type adding struct{ execution.Adapter }

func (adding) AddWorkspace(context.Context, execution.WorkspaceAddRequest) (execution.WorkspaceAdded, error) {
	return execution.WorkspaceAdded{}, nil
}

func TestWorkspaceAdderTrait(t *testing.T) {
	trait := execution.TraitOf[execution.WorkspaceAdder]()
	if trait != "workspaceAdder" {
		t.Fatalf("trait %q", trait)
	}
	if _, ok := execution.As[execution.WorkspaceAdder](adding{}); !ok {
		t.Fatal("adder not found")
	}
	if got := execution.Traits(adding{}); !slices.Contains(got, trait) {
		t.Fatalf("traits %v", got)
	}
	if _, ok := execution.As[execution.WorkspaceAdder](plain{}); ok {
		t.Fatal("plain adapter adds workspaces")
	}
	if !slices.Contains(execution.Ops, execution.OpWorkspaceAdd) || execution.Ops[len(execution.Ops)-1] != execution.OpWorkspaceAdd {
		t.Fatalf("ops %v", execution.Ops)
	}
	var uncertain *execution.OutcomeUncertain
	if err := error(&execution.OutcomeUncertain{Cause: context.DeadlineExceeded}); !errors.As(err, &uncertain) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("uncertain does not unwrap")
	}
}
