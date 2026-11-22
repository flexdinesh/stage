package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agenttools"
)

// stage setup installs the skill for detected agents (Claude Code and
// OpenCode here: codex is not on PATH), shrinks an old guide block to the
// pointer and registers MCP only with --mcp; --check writes nothing.
func TestSetupAgents(t *testing.T) {
	h := newHarness(t)
	h.home = t.TempDir()
	claudeMD := filepath.Join(h.home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(claudeMD), 0o755); err != nil {
		t.Fatal(err)
	}
	old := "# Mine\n\n<!-- stage:begin -->\n## stage (task queue)\n\nthe old full guide, afterMe and all\n<!-- stage:end -->\n"
	if err := os.WriteFile(claudeMD, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	claudeSkill := filepath.Join(h.home, ".claude", "skills", "stage", "SKILL.md")
	agentsSkill := filepath.Join(h.home, ".agents", "skills", "stage", "SKILL.md")

	code, stdout, _ := h.run("", "setup", "--check")
	if code != 1 || !strings.Contains(stdout, "missing "+claudeSkill) || !strings.Contains(stdout, "old Stage block") {
		t.Fatalf("check before setup: %d\n%s", code, stdout)
	}
	if _, err := os.Stat(claudeSkill); err == nil {
		t.Fatal("--check wrote the skill")
	}

	code, stdout, stderr := h.run("", "setup")
	if code != 0 || len(h.execs) != 0 {
		t.Fatalf("setup: %d %v\n%s%s", code, h.execs, stdout, stderr)
	}
	skill := agenttools.Skill("stage")
	for _, path := range []string{claudeSkill, agentsSkill} {
		if b, err := os.ReadFile(path); err != nil || string(b) != skill {
			t.Fatalf("skill %s: %v", path, err)
		}
	}
	if b, _ := os.ReadFile(claudeMD); !strings.HasPrefix(string(b), "# Mine\n\n") || strings.Contains(string(b), "afterMe") || !strings.Contains(string(b), "`stage` skill") {
		t.Fatalf("instructions not shrunk:\n%s", b)
	}
	if _, err := os.Stat(filepath.Join(h.home, ".codex")); err == nil {
		t.Fatal("configured an undetected agent")
	}

	code, stdout, _ = h.run("", "setup", "--check", "--json")
	if r := decode[struct {
		Problems int `json:"problems"`
		Agents   []struct {
			Agent string `json:"agent"`
		} `json:"agents"`
	}](t, stdout); code != 0 || r.Problems != 0 || len(r.Agents) != 2 {
		t.Fatalf("check after setup: %d %s", code, stdout)
	}

	code, _, stderr = h.run("", "setup", "--claude", "--mcp")
	if code != 0 || len(h.execs) != 2 || h.execs[1][0] != "claude" || !strings.Contains(strings.Join(h.execs[1], " "), "stage -- /usr/local/bin/stage mcp") {
		t.Fatalf("setup --mcp: %d %v %s", code, h.execs, stderr)
	}

	h.execs = nil
	codexSkill := filepath.Join(h.home, ".agents", "skills", "stage", "SKILL.md")
	if err := os.Remove(codexSkill); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := h.run("", "setup", "skill", "--codex"); code != 0 || len(h.execs) != 0 {
		t.Fatalf("setup skill: %d", code)
	}
	if _, err := os.Stat(codexSkill); err != nil {
		t.Fatal("setup skill --codex did not write ~/.agents/skills", err)
	}
}

func TestSkillNamesTheCLI(t *testing.T) {
	s := agenttools.Skill("stage-dev")
	if !strings.HasPrefix(s, "---\nname: stage-dev\ndescription: ") || !strings.Contains(s, "`stage-dev call NAME") || !strings.Contains(s, agenttools.Guide("stage-dev")) {
		t.Fatalf("skill:\n%s", s)
	}
}
