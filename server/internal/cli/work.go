package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/flexdinesh/stage/server/internal/agenttools"
)

// runTaskWork runs task preview|complete|reopen|requests|respond|send|
// opening-prompt: accepting another task and driving its session. Stage
// refuses each write on the caller's own task or session.
func runTaskWork(ctx context.Context, c *cmd, v string, rest []string) error {
	caller := c.sessionFlags("caller")
	callerArgs := func() (agenttools.CallerArgs, error) {
		ref, err := caller()
		return agenttools.CallerArgs{CallerSession: ref}, err
	}
	printTask := func(t any) error { return c.out(t, func(w io.Writer) { printAnyTask(w, t) }) }
	switch v {
	case "preview":
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		res, err := c.ops().GetCompletionPreview(ctx, agenttools.TaskIDArgs{TaskID: id})
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) {
			kv(w, "Revision", "%d", res.Revision)
			kv(w, "Evidence version", "%s", res.EvidenceVersion)
			if res.BlockedReason != "" {
				warn(w, "%s", res.BlockedReason)
			}
			if res.Evidence.Reply != nil {
				kv(w, "Reply", "%s", *res.Evidence.Reply)
			}
			for _, d := range res.Dependents {
				kv(w, "Dependent", "#%d %s", d.TaskID, d.Title)
			}
		})
	case "complete", "reopen":
		var revision int64
		var evidence, requestID, note string
		c.fs.Int64Var(&revision, "revision", 0, "revision from task preview")
		c.fs.StringVar(&requestID, "request-id", "", "idempotency key (default: derived from the task and revision)")
		if v == "complete" {
			c.fs.StringVar(&evidence, "evidence-version", "", "evidence version from task preview")
			c.fs.StringVar(&note, "note", "", "outcome note")
		}
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		if !c.set("revision") || (v == "complete" && evidence == "") {
			need := "--revision"
			if v == "complete" {
				need += " and --evidence-version"
			}
			return usagef("task %s: supply %s from `task preview %d`", v, need, id)
		}
		if requestID == "" {
			requestID = fmt.Sprintf("%s-%d-%d", v, id, revision)
		}
		ca, err := callerArgs()
		if err != nil {
			return err
		}
		ops := c.ops()
		var res any
		if v == "complete" {
			res, err = ops.CompleteTask(ctx, agenttools.CompleteTaskArgs{CallerArgs: ca, TaskID: id, Revision: revision, EvidenceVersion: evidence, RequestID: requestID, Note: note})
		} else {
			res, err = ops.ReopenTask(ctx, agenttools.ReopenTaskArgs{CallerArgs: ca, TaskID: id, Revision: revision, RequestID: requestID})
		}
		if err != nil {
			return err
		}
		done := "Completed"
		if v == "reopen" {
			done = "Reopened"
		}
		return c.out(res, func(w io.Writer) { success(w, "%s task %d", done, id) })
	case "requests":
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		res, err := c.ops().ListRequests(ctx, agenttools.TaskIDArgs{TaskID: id})
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) {
			if len(res.Requests) == 0 {
				fmt.Fprintln(w, "No pending requests")
			}
			for _, r := range res.Requests {
				kv(w, r.ID, "%s: %s", r.Kind, r.Title)
				for _, o := range r.Options {
					kv(w, "", "option %s (%s)", o.ID, o.Label)
				}
			}
		})
	case "respond":
		var a agenttools.RespondRequestArgs
		var answers string
		c.fs.StringVar(&a.Option, "option", "", "chosen option id")
		c.fs.StringVar(&a.Text, "text", "", "text answer")
		c.fs.StringVar(&answers, "answers", "", `JSON object of question id to option labels, e.g. {"q1":["Yes"]}`)
		if err := c.parse(rest); err != nil {
			return err
		}
		if len(c.pos) != 2 {
			return usagef("task respond: want a task id and a request id")
		}
		id, err := parseID(c.pos[0])
		if err != nil {
			return err
		}
		if answers != "" {
			if err := json.Unmarshal([]byte(answers), &a.Answers); err != nil {
				return usagef("task respond: --answers: %v", err)
			}
		}
		if a.CallerArgs, err = callerArgs(); err != nil {
			return err
		}
		a.TaskID, a.RequestID = id, c.pos[1]
		res, err := c.ops().RespondRequest(ctx, a)
		if err != nil {
			return err
		}
		return printTask(res)
	case "send":
		var text string
		c.fs.StringVar(&text, "text", "", "message text")
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		ca, err := callerArgs()
		if err != nil {
			return err
		}
		res, err := c.ops().SendMessage(ctx, agenttools.SendMessageArgs{CallerArgs: ca, TaskID: id, Text: text})
		if err != nil {
			return err
		}
		return printTask(res)
	case "opening-prompt":
		var full bool
		c.fs.BoolVar(&full, "full", false, "untruncated")
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		res, err := c.ops().GetOpeningPrompt(ctx, agenttools.OpeningPromptArgs{TaskID: id, Full: full})
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) {
			if res.Item == nil {
				fmt.Fprintln(w, "No opening prompt")
				return
			}
			fmt.Fprintln(w, res.Item.Text)
		})
	}
	return usagef("task %s: unknown", v)
}
