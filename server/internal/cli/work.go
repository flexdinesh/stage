package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// runTaskWork handles acceptance and session interaction commands. Stage
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
		expect := expectRevision(c)
		if err := c.parse(rest); err != nil {
			return err
		}
		a.ExpectedStateRevision = expect()
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
		var steer, queue bool
		c.fs.StringVar(&text, "text", "", "message text")
		c.fs.BoolVar(&steer, "steer", false, "inject into the running turn")
		c.fs.BoolVar(&queue, "queue", false, "queue for the next turn")
		expect := expectRevision(c)
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		var delivery execution.Delivery
		switch {
		case steer && queue:
			return usagef("task send: --steer and --queue are mutually exclusive")
		case steer:
			delivery = execution.DeliverySteer
		case queue:
			delivery = execution.DeliveryQueue
		}
		ca, err := callerArgs()
		if err != nil {
			return err
		}
		res, err := c.ops().SendMessage(ctx, agenttools.SendMessageArgs{CallerArgs: ca, TaskID: id, Text: text, Delivery: delivery, ExpectedStateRevision: expect()})
		if err != nil {
			return err
		}
		return printTask(res)
	case "activity":
		var limit, attempt int
		c.fs.IntVar(&limit, "limit", 0, "recent item count (default 30, capped at 100)")
		c.fs.IntVar(&attempt, "attempt", 0, "read this attempt's session (default current)")
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		in := agenttools.SessionActivityArgs{TaskID: id, Limit: limit}
		if c.set("attempt") {
			in.Attempt = &attempt
		}
		res, err := c.ops().GetSessionActivity(ctx, in)
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) { printActivity(w, res.Activity) })
	case "queue":
		var cancelID, steerID string
		c.fs.StringVar(&cancelID, "cancel", "", "cancel the queued message with this id")
		c.fs.StringVar(&steerID, "steer", "", "promote the queued message with this id to a steer")
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		in := agenttools.QueuedMessagesArgs{TaskID: id, Action: "list"}
		switch {
		case c.set("cancel") && c.set("steer"):
			return usagef("task queue: --cancel and --steer are mutually exclusive")
		case c.set("cancel"):
			in.Action, in.ID = "cancel", cancelID
		case c.set("steer"):
			in.Action, in.ID = "steer", steerID
		}
		if in.Action != "list" {
			if strings.TrimSpace(in.ID) == "" {
				return usagef("task queue: --%s requires a queued message id", in.Action)
			}
			if in.CallerArgs, err = callerArgs(); err != nil {
				return err
			}
		}
		res, err := c.ops().QueuedMessages(ctx, in)
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) {
			switch in.Action {
			case "cancel":
				success(w, "Cancelled queued message %s", in.ID)
			case "steer":
				success(w, "Steered queued message %s", in.ID)
			default:
				printQueue(w, res.Messages)
			}
		})
	case "escalate":
		var reason string
		c.fs.StringVar(&reason, "reason", "", "why the user must answer (at most 500 characters)")
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
		res, err := c.ops().EscalateToUser(ctx, agenttools.EscalateToUserArgs{CallerArgs: ca, TaskID: id, Reason: reason})
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

func printQueue(w io.Writer, messages []execution.QueuedMessage) {
	if len(messages) == 0 {
		fmt.Fprintln(w, "No queued messages")
	}
	for _, message := range messages {
		fmt.Fprintf(w, "%s  %s", message.ID, message.Delivery)
		if !message.CreatedAt.IsZero() {
			fmt.Fprintf(w, "  %s", message.CreatedAt.Format(time.RFC3339))
		}
		if message.Source != "" {
			fmt.Fprintf(w, "  %s", message.Source)
		}
		fmt.Fprintf(w, "\n  %s\n", message.Text)
	}
}

func printActivity(w io.Writer, a execution.Activity) {
	if len(a.Items) == 0 {
		fmt.Fprintln(w, "No recent activity")
	}
	for _, item := range a.Items {
		fmt.Fprintf(w, "%s  %s  %s", item.At.Format(time.RFC3339), item.Kind, item.Title)
		if item.Preview != "" {
			fmt.Fprintf(w, "  %s", item.Preview)
		}
		fmt.Fprintln(w)
	}
	if len(a.Plan) > 0 {
		fmt.Fprintln(w, "Plan")
		for _, step := range a.Plan {
			fmt.Fprintf(w, "  %s  %s\n", step.Status, step.Text)
		}
	}
	if len(a.Files) > 0 {
		fmt.Fprintln(w, "Files")
		for _, file := range a.Files {
			if file.CountsUnknown {
				fmt.Fprintf(w, "  %s\n", file.Path)
				continue
			}
			fmt.Fprintf(w, "  %s  +%d -%d %s\n", file.Path, file.Additions, file.Deletions, file.Status)
		}
	}
}

// expectRevision registers --expect-revision (a guarded answer: first
// answer wins) and returns its value after parsing (nil: not given).
func expectRevision(c *cmd) func() *int64 {
	var revision int64
	c.fs.Int64Var(&revision, "expect-revision", 0, "the task's state revision you are answering (task show); refused with already_answered when it moved on or was answered first")
	return func() *int64 {
		if !c.set("expect-revision") {
			return nil
		}
		return &revision
	}
}
