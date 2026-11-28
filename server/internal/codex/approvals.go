package codex

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

const (
	methodCommandApproval     = "item/commandExecution/requestApproval"
	methodFileApproval        = "item/fileChange/requestApproval"
	methodPermissionsApproval = "item/permissions/requestApproval"
	notifyResolved            = "serverRequest/resolved"
)

type approvalParams struct {
	ThreadID              string                     `json:"threadId"`
	TurnID                string                     `json:"turnId"`
	ItemID                string                     `json:"itemId"`
	ApprovalID            string                     `json:"approvalId"`
	Kind                  string                     `json:"kind"`
	Reason                string                     `json:"reason"`
	Command               string                     `json:"command"`
	Cwd                   string                     `json:"cwd"`
	GrantRoot             string                     `json:"grantRoot"`
	StartedAtMs           int64                      `json:"startedAtMs"`
	AvailableDecisions    []json.RawMessage          `json:"availableDecisions"`
	Permissions           map[string]json.RawMessage `json:"permissions"`
	AdditionalPermissions json.RawMessage            `json:"additionalPermissions"`
	NetworkContext        *struct {
		Host     string `json:"host"`
		Protocol string `json:"protocol"`
	} `json:"networkApprovalContext"`
}

type approval struct {
	rpcID   json.RawMessage
	method  string
	itemID  string
	turnID  string
	request execution.Request
	results map[string]any
}

type decision struct {
	id, label string
	result    any
}

var commandDecisions = []decision{
	{id: "accept", label: "Run command"},
	{id: "acceptForSession", label: "Run, and don't ask again for this command"},
	{id: "decline", label: "Don't run"},
	{id: "cancel", label: "Stop the turn"},
}

var fileDecisions = []decision{
	{id: "accept", label: "Apply changes"},
	{id: "acceptForSession", label: "Apply, and don't ask again for these files"},
	{id: "decline", label: "Don't apply"},
	{id: "cancel", label: "Stop the turn"},
}

func decodeApproval(r serverRequest) (approvalParams, *approval) {
	var p approvalParams
	switch r.Method {
	case methodCommandApproval, methodFileApproval, methodPermissionsApproval:
	default:
		return p, nil
	}
	if json.Unmarshal(r.Params, &p) != nil || p.ItemID == "" || p.ThreadID == "" {
		return p, nil
	}
	id := p.ApprovalID
	if id == "" {
		id = p.ItemID
	}
	a := &approval{rpcID: r.ID, method: r.Method, itemID: p.ItemID, turnID: p.TurnID, results: map[string]any{}}
	a.request = execution.Request{ID: id, Kind: execution.RequestApproval}
	if p.StartedAtMs > 0 {
		a.request.CreatedAt = time.UnixMilli(p.StartedAtMs).UTC()
	}
	var detail []string
	if reason := strings.TrimSpace(p.Reason); reason != "" {
		detail = append(detail, reason)
	}
	if p.Cwd != "" {
		detail = append(detail, "Working directory "+p.Cwd)
	}
	switch r.Method {
	case methodCommandApproval:
		a.request.Title, a.request.Preview = "Run a command", p.Command
		if extra := p.AdditionalPermissions; len(extra) > 0 && string(extra) != "null" {
			detail = append(detail, "Additional permissions "+string(extra))
		}
		var labels map[string]string
		if n := p.NetworkContext; n != nil && n.Host != "" {
			a.request.Title, a.request.Preview = "Allow network access", n.Host+" ("+n.Protocol+")"
			labels = map[string]string{"accept": "Allow access", "acceptForSession": "Allow access for this session", "decline": "Deny access"}
		} else if p.Kind == "writeStdin" {
			a.request.Title = "Send input to a command"
			labels = map[string]string{"accept": "Send input", "acceptForSession": "Allow input for this session", "decline": "Don't send input"}
		}
		for _, d := range offeredCommandDecisions(p.AvailableDecisions) {
			if label := labels[d.id]; label != "" {
				d.label = label
			}
			a.add(d)
		}
	case methodFileApproval:
		a.request.Title = "Apply file changes"
		if p.GrantRoot != "" {
			detail = append(detail, "Codex requests write access under "+p.GrantRoot+" for this session.")
		}
		for _, d := range fileDecisions {
			a.add(d)
		}
	case methodPermissionsApproval:
		a.request.Title = "Grant permissions"
		permissions := make(map[string]json.RawMessage)
		for key, value := range p.Permissions {
			if string(value) != "null" {
				permissions[key] = value
			}
		}
		if len(permissions) == 0 {
			return p, nil
		}
		preview, err := json.MarshalIndent(permissions, "", "  ")
		if err != nil {
			return p, nil
		}
		a.request.Preview = string(preview)
		a.add(decision{"turn", "Allow for this turn", map[string]any{"permissions": permissions, "scope": "turn"}})
		a.add(decision{"session", "Allow for this session", map[string]any{"permissions": permissions, "scope": "session"}})
		a.add(decision{"decline", "Don't grant permissions", map[string]any{"permissions": struct{}{}, "scope": "turn"}})
	}
	if len(a.results) == 0 {
		return p, nil
	}
	a.request.Detail = strings.Join(detail, "\n")
	return p, a
}

func (a *approval) add(d decision) {
	if _, exists := a.results[d.id]; exists {
		return
	}
	a.request.Options = append(a.request.Options, execution.Choice{ID: d.id, Label: d.label, Availability: available})
	result := d.result
	if result == nil {
		result = map[string]any{"decision": d.id}
	}
	a.results[d.id] = result
}

func (a *approval) answer(r execution.Response) (any, error) {
	if len(r.Answers) > 0 || strings.TrimSpace(r.Text) != "" {
		return nil, &execution.Invalid{Reason: "Pick one of the options for this Codex approval."}
	}
	result, ok := a.results[r.Option]
	if !ok {
		return nil, &execution.Invalid{Reason: fmt.Sprintf("Codex does not offer %q for this request.", r.Option)}
	}
	return result, nil
}

// An absent list has the plain defaults. An explicit list limits the choices
// to the decisions Codex sent, in order, with amendment payloads intact.
func offeredCommandDecisions(offered []json.RawMessage) []decision {
	if offered == nil {
		return commandDecisions
	}
	var out []decision
	for _, raw := range offered {
		var plain string
		if json.Unmarshal(raw, &plain) == nil {
			if i := slices.IndexFunc(commandDecisions, func(d decision) bool { return d.id == plain }); i >= 0 {
				out = append(out, commandDecisions[i])
			}
		} else if d, ok := amendmentDecision(raw); ok {
			out = append(out, d)
		}
	}
	return out
}

func amendmentDecision(raw json.RawMessage) (decision, bool) {
	var d struct {
		Exec *struct {
			Prefix []string `json:"execpolicy_amendment"`
		} `json:"acceptWithExecpolicyAmendment"`
		Network *struct {
			Rule struct {
				Host   string `json:"host"`
				Action string `json:"action"`
			} `json:"network_policy_amendment"`
		} `json:"applyNetworkPolicyAmendment"`
	}
	if json.Unmarshal(raw, &d) != nil {
		return decision{}, false
	}
	result := map[string]any{"decision": raw}
	if d.Exec != nil && len(d.Exec.Prefix) > 0 && d.Network == nil {
		args, _ := json.Marshal(d.Exec.Prefix)
		return decision{"acceptWithExecpolicyAmendment", "Run and always allow command prefix " + string(args), result}, true
	}
	if d.Network != nil && d.Exec == nil && d.Network.Rule.Host != "" {
		rule := d.Network.Rule
		id := "applyNetworkPolicyAmendment:" + rule.Action + ":" + rule.Host
		switch rule.Action {
		case "allow":
			return decision{id, "Always allow network access to " + rule.Host, result}, true
		case "deny":
			return decision{id, "Always deny network access to " + rule.Host, result}, true
		}
	}
	return decision{}, false
}
