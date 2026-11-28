package codextest

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Config writes as codex-cli 0.172.0 answers them (verified on a throwaway
// CODEX_HOME): errors are -32600 with data.config_write_error_code; a key
// path splits on unquoted dots; upsert deep-merges tables.

// emptyVersion is Codex's version for an empty layer (sha256 of "").
const emptyVersion = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

type override struct{ level, message string }

// SetProject replaces a project's whole entry in the user config.
func (s *Server) SetProject(path string, entry map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projects[path] = maps.Clone(entry)
}

// Project is a copy of a project's user config entry (nil: none).
func (s *Server) Project(path string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.projects[path])
}

// ProjectPaths are the user config's project keys, sorted.
func (s *Server) ProjectPaths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Sorted(maps.Keys(s.projects))
}

// ConcurrentWrites makes the next n batch writes find another client's
// write first (a version conflict when they send expectedVersion).
func (s *Server) ConcurrentWrites(n int) { s.mu.Lock(); s.concurrentWrites = n; s.mu.Unlock() }

// ReadonlyConfig refuses config writes as a read-only layer would.
func (s *Server) ReadonlyConfig(v bool) { s.mu.Lock(); s.readonlyConfig = v; s.mu.Unlock() }

// Override makes a higher layer decide path's trust level ("": the layer
// leaves the level alone); writes that touch path answer okOverridden with
// message.
func (s *Server) Override(path, level, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.overrides[path] = override{level: level, message: message}
}

func (s *Server) userVersion() string {
	if s.configVersion == 0 {
		return emptyVersion
	}
	return fmt.Sprintf("sha256:%064d", s.configVersion)
}

func (s *Server) configRead(includeLayers bool) map[string]any {
	projects := map[string]any{}
	for path, entry := range s.projects {
		projects[path] = maps.Clone(entry)
	}
	for path, o := range s.overrides {
		if o.level == "" {
			continue
		}
		entry, _ := projects[path].(map[string]any)
		if entry == nil {
			entry = map[string]any{}
		}
		entry["trust_level"] = o.level
		projects[path] = entry
	}
	res := map[string]any{"config": map[string]any{"model": "gpt-test", "projects": projects}, "origins": map[string]any{}, "layers": nil}
	if includeLayers {
		res["layers"] = []map[string]any{
			{"name": map[string]any{"type": "user", "file": s.CodexHome + "/config.toml", "profile": nil}, "version": s.userVersion(), "config": map[string]any{}},
			{"name": map[string]any{"type": "system", "file": "/etc/codex/config.toml"}, "version": emptyVersion, "config": map[string]any{}},
		}
	}
	return res
}

func writeError(code, message string) *rpcError {
	return &rpcError{Code: -32600, Message: message, Data: map[string]string{"config_write_error_code": code}}
}

func (s *Server) configBatchWrite(raw json.RawMessage) (any, *rpcError) {
	var p struct {
		Edits []struct {
			KeyPath       string          `json:"keyPath"`
			Value         json.RawMessage `json:"value"`
			MergeStrategy string          `json:"mergeStrategy"`
		} `json:"edits"`
		FilePath        *string `json:"filePath"`
		ExpectedVersion *string `json:"expectedVersion"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, invalid("Invalid request: %v", err)
	}
	if s.readonlyConfig || (p.FilePath != nil && *p.FilePath != "" && *p.FilePath != s.CodexHome+"/config.toml") {
		return nil, writeError("configLayerReadonly", "Only writes to the user config are allowed")
	}
	if s.concurrentWrites > 0 {
		s.concurrentWrites--
		s.configVersion++
	}
	if p.ExpectedVersion != nil && *p.ExpectedVersion != s.userVersion() {
		return nil, writeError("configVersionConflict", "Configuration was modified since last read. Fetch latest version and retry.")
	}
	current := map[string]any{}
	for path, entry := range s.projects {
		current[path] = deepClone(entry)
	}
	root := map[string]any{"projects": current}
	overridden := ""
	for _, e := range p.Edits {
		var v any
		if err := json.Unmarshal(e.Value, &v); err != nil {
			return nil, invalid("Invalid request: %v", err)
		}
		keys := splitKeyPath(e.KeyPath)
		if len(keys) == 0 || keys[0] != "projects" {
			return nil, writeError("configValidationError", "the fake daemon writes only projects")
		}
		set(root, keys, v, e.MergeStrategy == "upsert")
		for path := range s.overrides {
			if touches(keys, v, path) {
				overridden = s.overrides[path].message
			}
		}
	}
	projects, ok := root["projects"].(map[string]any)
	if !ok {
		return nil, writeError("configValidationError", "Invalid configuration: projects must be a table")
	}
	next := map[string]map[string]any{}
	for path, raw := range projects {
		entry, ok := raw.(map[string]any)
		if !ok {
			return nil, writeError("configValidationError", fmt.Sprintf("Invalid configuration: invalid type\nin `projects.%s`\n", path))
		}
		if level, has := entry["trust_level"]; has && level != "trusted" && level != "untrusted" {
			return nil, writeError("configValidationError", fmt.Sprintf("Invalid configuration: unknown variant `%v`, expected `trusted` or `untrusted`\nin `projects.%s.trust_level`\n", level, path))
		}
		next[path] = entry
	}
	s.projects = next
	s.configVersion++
	res := map[string]any{"status": "ok", "version": s.userVersion(), "filePath": s.CodexHome + "/config.toml", "overriddenMetadata": nil}
	if overridden != "" {
		res["status"] = "okOverridden"
		res["overriddenMetadata"] = map[string]any{"message": overridden, "overridingLayer": map[string]any{"name": map[string]any{"type": "enterpriseManaged", "id": "m", "name": "Managed"}, "version": "sha256:managed"}, "effectiveValue": nil}
	}
	return res, nil
}

// splitKeyPath splits on dots outside double quotes, as Codex does: an
// unquoted dotted project path becomes nested tables.
func splitKeyPath(kp string) []string {
	var out []string
	var cur strings.Builder
	quoted := false
	for _, r := range kp {
		switch {
		case r == '"':
			quoted = !quoted
		case r == '.' && !quoted:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	return append(out, cur.String())
}

// set writes v at keys under root; upsert deep-merges tables.
func set(root map[string]any, keys []string, v any, upsert bool) {
	m := root
	for _, k := range keys[:len(keys)-1] {
		next, ok := m[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[k] = next
		}
		m = next
	}
	last := keys[len(keys)-1]
	if cur, ok := m[last].(map[string]any); ok && upsert {
		if nv, ok := v.(map[string]any); ok {
			merge(cur, nv)
			return
		}
	}
	m[last] = v
}

func merge(dst, src map[string]any) {
	for k, v := range src {
		if d, ok := dst[k].(map[string]any); ok {
			if sv, ok := v.(map[string]any); ok {
				merge(d, sv)
				continue
			}
		}
		dst[k] = v
	}
}

func deepClone(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if sub, ok := v.(map[string]any); ok {
			v = deepClone(sub)
		}
		out[k] = v
	}
	return out
}

// touches reports whether an edit writes path's projects entry.
func touches(keys []string, v any, path string) bool {
	if len(keys) >= 2 {
		return keys[1] == path
	}
	m, ok := v.(map[string]any)
	return ok && m[path] != nil
}

func fsMetadata(path string) (any, *rpcError) {
	if !filepath.IsAbs(path) {
		return nil, invalid("Invalid request: AbsolutePathBuf deserialized without a base path")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: "No such file or directory (os error 2)"}
	}
	return map[string]any{"isDirectory": info.IsDir(), "isFile": info.Mode().IsRegular(), "isSymlink": false, "createdAtMs": 0, "modifiedAtMs": info.ModTime().UnixMilli()}, nil
}

// fsReadDirectory answers fs/readDirectory from the real filesystem as Codex
// 0.172.0 does (verified on a throwaway app-server): direct children by
// name, hidden ones included, a symlink by its target's kind and a broken
// one left out; a missing folder, a file or an unreadable folder is -32603
// with the OS error.
func fsReadDirectory(path string) (any, *rpcError) {
	if !filepath.IsAbs(path) {
		return nil, invalid("Invalid request: AbsolutePathBuf deserialized without a base path")
	}
	info, err := os.Stat(path)
	switch {
	case err != nil:
		return nil, &rpcError{Code: -32603, Message: "No such file or directory (os error 2)"}
	case !info.IsDir():
		return nil, &rpcError{Code: -32603, Message: "Not a directory (os error 20)"}
	}
	names, err := os.ReadDir(path)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: "Permission denied (os error 13)"}
	}
	entries := []map[string]any{}
	for _, e := range names {
		target, err := os.Stat(filepath.Join(path, e.Name()))
		if err != nil {
			continue
		}
		entries = append(entries, map[string]any{"fileName": e.Name(), "isDirectory": target.IsDir(), "isFile": target.Mode().IsRegular()})
	}
	return map[string]any{"entries": entries}, nil
}
