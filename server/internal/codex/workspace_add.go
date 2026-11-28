package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// SourceTrust is Codex's workspace source: trust an existing folder, which
// lists it among the workspaces (ADR 0012, superseding ADR 0006 D8).
const SourceTrust = "trust"

var trustSource = execution.WorkspaceSource{
	ID:    SourceTrust,
	Label: "Trust folder",
	Input: []execution.Field{{
		ID: "path", Label: "Folder", Kind: execution.FieldPath, Required: true,
		Help: "Absolute path on the Codex host.",
	}},
	Confirm:      "Codex will trust this folder: it applies the folder's Codex project config. This also applies to the Codex CLI.",
	Availability: available,
}

// Codex's config write error codes (RPCError data config_write_error_code).
const (
	writeVersionConflict = "configVersionConflict"
	writeLayerReadonly   = "configLayerReadonly"
	writeValidation      = "configValidationError"
)

var _ execution.WorkspaceAdder = (*Adapter)(nil)

// AddWorkspace trusts a folder through the daemon's own config API
// (config/batchWrite), never by editing config.toml. A folder Codex already
// trusts is Existing. The write upserts the projects table with the path as
// a key, so dots in the path never split it into nested keys.
func (a *Adapter) AddWorkspace(ctx context.Context, r execution.WorkspaceAddRequest) (execution.WorkspaceAdded, error) {
	var out execution.WorkspaceAdded
	if r.Source != SourceTrust {
		return out, &execution.Invalid{Reason: fmt.Sprintf("Codex has no workspace source %q.", r.Source)}
	}
	var in struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(r.Input, &in); err != nil {
		return out, &execution.Invalid{Reason: "Malformed Trust folder input."}
	}
	if !filepath.IsAbs(in.Path) {
		return out, &execution.Invalid{Reason: "Enter an absolute folder path on the Codex host."}
	}
	if !r.Confirmed {
		return out, &execution.Invalid{Reason: "Confirm before Codex trusts this folder."}
	}
	dir := filepath.Clean(in.Path)

	var meta fsMetadata
	if err := a.client.call(ctx, "fs/getMetadata", map[string]any{"path": dir}, &meta); err != nil {
		var re *RPCError
		if errors.As(err, &re) && !unsupported(err) {
			return out, &execution.Invalid{Reason: "Codex cannot read " + dir + ": " + re.Message}
		}
		return out, a.addError(err)
	}
	if !meta.IsDirectory {
		return out, &execution.Invalid{Reason: dir + " is not a folder on the Codex host."}
	}

	var written configWriteResponse
	for attempt := 0; ; attempt++ {
		res, err := a.readConfig(ctx, true)
		if err != nil {
			return out, a.addError(err)
		}
		ok, err := trusts(res.Config, dir)
		if err != nil {
			return out, err
		}
		if ok {
			return execution.WorkspaceAdded{Workspace: workspace(dir), Existing: true, Message: "Codex already trusts this folder."}, nil
		}
		params := map[string]any{"edits": []map[string]any{{
			"keyPath":       "projects",
			"value":         map[string]any{dir: map[string]string{"trust_level": trustTrusted}},
			"mergeStrategy": "upsert",
		}}}
		if v := userLayerVersion(res.Layers); v != "" {
			params["expectedVersion"] = v
		}
		err = a.client.call(ctx, "config/batchWrite", params, &written)
		if err == nil {
			break
		}
		var re *RPCError
		switch code := writeErrorCode(err); {
		case code == writeVersionConflict && attempt == 0:
			continue
		case code == writeVersionConflict:
			return out, errors.New("codex config kept changing while Stage trusted the folder; try again")
		case (code == writeLayerReadonly || code == writeValidation) && errors.As(err, &re):
			return out, &execution.Invalid{Reason: "Codex refused to trust the folder: " + re.Message}
		case errors.Is(err, errClosed), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			// The write may have landed: a retry finds it trusted.
			return out, &execution.OutcomeUncertain{Cause: err}
		}
		return out, a.addError(err)
	}

	var facts []execution.Fact
	override := ""
	if written.Status == "okOverridden" {
		override = "another Codex config layer overrides it"
		if written.Override != nil && written.Override.Message != "" {
			override = written.Override.Message
		}
		facts = append(facts, execution.Fact{ID: "overridden", Label: "Overridden", Value: override, Tone: execution.ToneWarn})
	}
	res, err := a.readConfig(ctx, false)
	if err != nil {
		return out, &execution.OutcomeUncertain{Cause: err}
	}
	ok, err := trusts(res.Config, dir)
	if err != nil {
		return out, &execution.OutcomeUncertain{Cause: err}
	}
	if !ok {
		if override == "" {
			override = "its effective config does not list it"
		}
		return out, &execution.Invalid{Reason: "Codex saved the trust, but " + override + "; Codex does not trust this folder."}
	}
	return execution.WorkspaceAdded{Workspace: workspace(dir), Message: "Codex trusts this folder now.", Facts: facts}, nil
}

// trusts reports whether the effective config trusts dir, as Workspaces
// lists it (a cleaned absolute key).
func trusts(config map[string]json.RawMessage, dir string) (bool, error) {
	projects, err := projectTrust(config)
	if err != nil {
		return false, err
	}
	for p, level := range projects {
		if level == trustTrusted && filepath.IsAbs(p) && filepath.Clean(p) == dir {
			return true, nil
		}
	}
	return false, nil
}

// userLayerVersion is the version of the user config.toml layer (not a
// profile), which a default write targets; "" when Codex reports none.
func userLayerVersion(layers []configLayer) string {
	for _, l := range layers {
		if l.Name.Type == "user" && l.Name.Profile == nil {
			return l.Version
		}
	}
	return ""
}

// writeErrorCode is a config write's config_write_error_code, if any.
func writeErrorCode(err error) string {
	var re *RPCError
	if !errors.As(err, &re) || len(re.Data) == 0 {
		return ""
	}
	var d struct {
		Code string `json:"config_write_error_code"`
	}
	if json.Unmarshal(re.Data, &d) != nil {
		return ""
	}
	return d.Code
}

// addError keeps the reason a missing method or an unreachable daemon
// gives; nothing was written.
func (a *Adapter) addError(err error) error {
	if unsupported(err) {
		return fmt.Errorf("%w: %s", execution.ErrUnavailable, Problem(err))
	}
	return err
}
