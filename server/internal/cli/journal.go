package cli

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"strings"

	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/db"
)

// runNotes accepts notes OWNER_ID [list|add|get], with flags anywhere.
func runNotes(ctx context.Context, env Env, kind string, args []string) error {
	c := newCmd(env, kind+" notes")
	caller := c.sessionFlags("caller")
	var read agenttools.ReadJournalArgs
	var appendArgs agenttools.AppendNoteArgs
	var filterTask, sourceTask int64
	c.fs.StringVar(&read.Kind, "kind", "", "notes|events|all (default notes)")
	c.fs.StringVar(&read.Category, "category", "", "category")
	c.fs.StringVar(&read.Q, "q", "", "search text")
	c.fs.StringVar(&read.Cursor, "cursor", "", "next page cursor")
	c.fs.IntVar(&read.Limit, "limit", 20, "page size (1-100)")
	if kind == "tree" {
		c.fs.Int64Var(&filterTask, "task", 0, "filter to a current member task")
	}
	c.fs.StringVar(&appendArgs.Text, "text", "", "note text (- = stdin)")
	c.fs.StringVar(&appendArgs.RequestID, "request-id", "", "idempotency key (default: generated once)")
	c.fs.Int64Var(&sourceTask, "source-task", 0, "current member task contributing this tree note")
	if err := c.parse(args); err != nil {
		return err
	}
	if len(c.pos) == 0 {
		return usagef("%s notes: want an owner id", kind)
	}
	id, err := parseID(c.pos[0])
	if err != nil {
		return err
	}
	owner := agenttools.JournalOwnerArgs{}
	if kind == "tree" {
		owner.TreeID = &id
	} else {
		owner.TaskID = &id
	}
	action := "list"
	if len(c.pos) > 1 {
		action = c.pos[1]
	}
	switch action {
	case "list", "ls":
		if len(c.pos) > 2 || c.set("text") || c.set("request-id") || c.set("source-task") {
			return usagef("%s notes list: unexpected arguments or append flags", kind)
		}
		if read.Limit < 1 {
			return usagef("--limit must be between 1 and 100")
		}
		read.JournalOwnerArgs = owner
		read.FilterTaskID = int64Flag(c, "task", filterTask)
		page, err := c.ops().ReadJournal(ctx, read)
		if err != nil {
			return err
		}
		return c.out(page, func(w io.Writer) {
			for _, entry := range page.Entries {
				printJournalEntry(w, entry)
			}
			if page.NextCursor != nil {
				fmt.Fprintf(w, "next cursor: %s\n", *page.NextCursor)
			}
		})
	case "add", "append":
		if len(c.pos) != 2 || !c.set("text") || c.set("kind") || c.set("q") || c.set("cursor") || c.set("limit") || c.set("task") {
			return usagef("%s notes ID add: want --text, optional --category, --request-id, --source-task", kind)
		}
		if appendArgs.Text == "-" {
			body, err := io.ReadAll(io.LimitReader(c.env.Stdin, 16*1024+1))
			if err != nil {
				return err
			}
			appendArgs.Text = string(body)
		}
		if !c.set("request-id") {
			appendArgs.RequestID = rand.Text()
		}
		appendArgs.CallerSession, err = caller()
		if err != nil {
			return err
		}
		appendArgs.JournalOwnerArgs = owner
		appendArgs.Category = read.Category
		appendArgs.SourceTaskID = int64Flag(c, "source-task", sourceTask)
		entry, err := c.ops().AppendNote(ctx, appendArgs)
		if err != nil {
			return err
		}
		return c.out(entry, func(w io.Writer) { printJournalEntry(w, entry) })
	case "get", "show":
		if len(c.pos) != 3 || c.set("kind") || c.set("category") || c.set("q") || c.set("cursor") || c.set("limit") || c.set("task") || c.set("text") || c.set("request-id") || c.set("source-task") {
			return usagef("%s notes ID get: want one entry id", kind)
		}
		entryID, err := parseID(c.pos[2])
		if err != nil {
			return err
		}
		entry, err := c.ops().GetJournalEntry(ctx, agenttools.JournalEntryArgs{ID: entryID})
		if err != nil {
			return err
		}
		return c.out(entry, func(w io.Writer) { printJournalEntry(w, entry) })
	default:
		return usagef("unknown notes operation %q", action)
	}
}

func printJournalEntry(w io.Writer, entry db.JournalEntry) {
	fmt.Fprintf(w, "#%d  %s  %s  %s\n", entry.ID, entry.Kind, dash(entry.Category), ts(&entry.CreatedAt))
	var source []string
	if entry.TaskID != nil {
		source = append(source, fmt.Sprintf("task #%d", *entry.TaskID))
	}
	if entry.TreeID != nil {
		source = append(source, fmt.Sprintf("tree #%d", *entry.TreeID))
	}
	if entry.SourceTaskID != nil {
		source = append(source, fmt.Sprintf("source task #%d", *entry.SourceTaskID))
	}
	if entry.SourceSession != nil {
		source = append(source, fmt.Sprintf("connection %d session %s", entry.SourceSession.ConnectionID, entry.SourceSession.ExternalSessionID))
	} else if entry.SourceThreadID != nil {
		source = append(source, "session "+*entry.SourceThreadID)
	}
	if len(source) > 0 {
		fmt.Fprintln(w, "  "+strings.Join(source, "; "))
	}
	if entry.FromTreeID != nil || entry.ToTreeID != nil {
		from, to := "standalone", "standalone"
		if entry.FromTreeID != nil {
			from = fmt.Sprintf("tree #%d", *entry.FromTreeID)
		}
		if entry.ToTreeID != nil {
			to = fmt.Sprintf("tree #%d", *entry.ToTreeID)
		}
		fmt.Fprintf(w, "  %s -> %s\n", from, to)
	}
	if entry.EventType != "" {
		fmt.Fprintf(w, "  %s\n", entry.EventType)
	}
	fmt.Fprintln(w, strings.TrimRight(entry.Text, "\n"))
	if entry.Truncated {
		fmt.Fprintln(w, "[preview; use notes ID get ENTRY_ID for full text]")
	}
}
