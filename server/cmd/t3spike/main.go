// Command t3spike verifies the real T3 client against a live T3 (read-only):
// full OAuth with the admin token, then list projects, one page of threads,
// read one thread, and capabilities. It never launches, sends, interrupts or
// creates anything.
//
//	T3_ADMIN_TOKEN=... go run ./cmd/t3spike [-url http://127.0.0.1:3773] [-raw dir]
//
// T3_ADMIN_TOKEN is read from the environment or, if unset, from -env (.env).
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/flexdinesh/t3-stage/server/internal/t3"
)

func main() {
	baseURL := flag.String("url", envOr("T3_URL", "http://127.0.0.1:3773"), "T3 base URL")
	publicURL := flag.String("public", envOr("T3_PUBLIC_URL", "http://controlbox:3773"), "T3 public URL")
	envFile := flag.String("env", "../.env", "dotenv file to read T3_ADMIN_TOKEN from when unset")
	pairCode := flag.String("pair", "", "use this pairing code instead of minting one with the admin token")
	rawDir := flag.String("raw", "", "if set, write raw tool result JSON here")
	flag.Parse()

	admin := os.Getenv("T3_ADMIN_TOKEN")
	if admin == "" {
		admin = readDotenv(*envFile, "T3_ADMIN_TOKEN")
	}
	if admin == "" && *pairCode == "" {
		fail("T3_ADMIN_TOKEN not set (env or %s) and no -pair code", *envFile)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	store := t3.NewMemoryTokenStore()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	c := t3.NewMCPClient(t3.Options{BaseURL: *baseURL, AdminToken: admin, PublicURL: *publicURL, Store: store, Logger: log})
	defer c.Close()

	start := time.Now()
	if *pairCode != "" {
		must("pair", c.Pair(ctx, *pairCode))
	}
	projects, err := c.ListProjects(ctx)
	must("ListProjects", err)
	st := c.Status(ctx)
	fmt.Printf("oauth+connect: ok in %s; connected=%v clientIdLen=%d expiresAt=%s lastError=%q tokenSaves=%d\n",
		time.Since(start).Round(time.Millisecond), st.Connected, len(st.ClientID), fmtTime(st.ExpiresAt), st.LastError, store.Saves())
	fmt.Printf("environmentId: %s\n", st.EnvironmentID)

	archived := 0
	for _, p := range projects {
		if p.ArchivedAt != nil {
			archived++
		}
	}
	fmt.Printf("projects: %d (archived %d)\n", len(projects), archived)
	if len(projects) > 0 {
		p := projects[0]
		fmt.Printf("  sample: id=%s title=%q root=%s created=%s\n", p.ID, p.Title, p.WorkspaceRoot, p.CreatedAt.Format(time.RFC3339))
	}

	page, err := c.ListThreads(ctx, t3.ListThreadsInput{AllProjects: true, Limit: 100})
	must("ListThreads", err)
	next := "nil"
	if page.NextCursor != nil {
		next = fmt.Sprint(*page.NextCursor)
	}
	statuses := map[t3.ThreadStatus]int{}
	settled := 0
	for _, t := range page.Threads {
		statuses[t.Status]++
		if t.Settled {
			settled++
		}
	}
	fmt.Printf("threads page: %d of total %d, nextCursor=%s, settled=%d, statuses=%v\n", len(page.Threads), page.Total, next, settled, statuses)
	if len(page.Threads) == 0 {
		fail("no threads to read")
	}
	t := page.Threads[0]
	fmt.Printf("  sample: id=%s project=%s status=%s settled=%v provider=%s model=%s runtime=%s interaction=%s source=%s link=%s\n",
		t.ThreadID, t.ProjectID, t.Status, t.Settled, t.ProviderInstanceID, t.Model, t.RuntimeMode, t.InteractionMode, t.CreationSource, t.Link)

	d, err := c.ReadThread(ctx, t.ThreadID)
	must("ReadThread", err)
	fmt.Printf("read thread: status=%s settled=%v pending=%d runCount=%d activeRun=%q branch=%q worktree=%q recentRuns=%d\n",
		d.Status, d.Settled, d.PendingRequestCount, d.RunCount, d.ActiveRunID, d.Branch, d.WorktreePath, len(d.RecentRuns))
	if len(d.RecentRuns) > 0 {
		r := d.RecentRuns[0]
		fmt.Printf("  latest run: id=%s ordinal=%d status=%s model=%s completedAt=%s\n", r.RunID, r.Ordinal, r.Status, r.Model, fmtTime(r.CompletedAt))
	}
	fmt.Printf("web url: %s\n", c.WebURL(ctx, t.ThreadID))

	_, err = c.ReadThread(ctx, "thread-does-not-exist-t3spike")
	fmt.Printf("read unknown thread: err=%v\n", err)

	cat, err := c.Capabilities(ctx)
	must("Capabilities", err)
	fmt.Printf("capabilities: %d providers\n", len(cat.Providers))
	for _, p := range cat.Providers {
		opts := ""
		if len(p.Models) > 0 && len(p.Models[0].Options) > 0 {
			o := p.Models[0].Options[0]
			opts = fmt.Sprintf(" firstModelOpt=%s(%d values)", o.ID, len(o.Values))
		}
		fmt.Printf("  %s driver=%s name=%q models=%d runtimeModes=%d constraints=%v%s\n",
			p.ProviderInstanceID, p.DriverKind, p.DisplayName, len(p.Models), len(p.RuntimeModes), p.Constraints, opts)
	}

	if *rawDir != "" {
		dumpRaw(ctx, c, *rawDir, t.ThreadID)
	}
}

func dumpRaw(ctx context.Context, c *t3.MCPClient, dir, threadID string) {
	must("mkdir", os.MkdirAll(dir, 0o755))
	calls := map[string]struct {
		tool string
		args map[string]any
	}{
		"project_list.json": {"t3_project_list", map[string]any{"limit": 2}},
		"thread_list.json":  {"t3_thread_list", map[string]any{"allProjects": true, "limit": 3}},
		"thread_read.json":  {"t3_thread_read", map[string]any{"threadId": threadID, "limit": 1, "runLimit": 1, "maxCharsPerItem": 200}},
		"capabilities.json": {"orchestrator_capabilities", map[string]any{}},
	}
	for name, cl := range calls {
		raw, err := c.CallRaw(ctx, cl.tool, cl.args)
		must(cl.tool, err)
		var v any
		must("decode", json.Unmarshal(raw, &v))
		b, _ := json.MarshalIndent(v, "", "  ")
		must("write", os.WriteFile(filepath.Join(dir, name), b, 0o644))
	}
	fmt.Printf("raw results written to %s\n", dir)
}

func fmtTime(t *time.Time) string {
	if t == nil {
		return "nil"
	}
	return t.Format(time.RFC3339)
}

func must(what string, err error) {
	if err != nil {
		fail("%s: %v", what, err)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "t3spike: "+format+"\n", args...)
	os.Exit(1)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func readDotenv(path, key string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if v, ok := strings.CutPrefix(line, key+"="); ok {
			return strings.Trim(v, `"'`)
		}
	}
	return ""
}
