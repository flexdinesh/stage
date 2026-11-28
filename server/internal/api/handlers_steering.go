package api

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// Tree steering (docs/api.md "Tree steering", ADR 0013).

// setSteererReq is POST /api/trees/{id}/steerer.
type setSteererReq struct {
	Prompt          string            `json:"prompt"`
	Title           string            `json:"title"`
	ConnectionID    *int64            `json:"connectionId"`
	WorkspaceID     string            `json:"workspaceId"`
	ExecutionConfig *execution.Config `json:"executionConfig"`
	Replace         bool              `json:"replace"`
	RequestID       string            `json:"requestId"`
}

// steererOut is the set and stop steering response.
type steererOut struct {
	Tree     treeDetailJSON `json:"tree"`
	Task     *taskJSON      `json:"task,omitempty"`
	Warnings []string       `json:"warnings"`
}

// setSteerer creates a standalone steerer task, binds it to the tree and
// launches it (replace: ending and stopping the current steerer first).
func (s *server) setSteerer(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var req setSteererReq
	if err := decodeConfig(r, &req, true); err != nil {
		return err
	}
	if strings.TrimSpace(req.RequestID) == "" {
		return db.Invalid("requestId is required")
	}
	if strings.TrimSpace(req.WorkspaceID) == "" {
		return db.DestinationRequired()
	}
	ctx := r.Context()
	tree, err := s.Store.GetTree(ctx, id)
	if err != nil {
		return err
	}
	digest, err := steererDigest(req)
	if err != nil {
		return err
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "Steer: " + tree.Title
	}
	cfg := taskConfigReq{
		ConnectionID: req.ConnectionID, WorkspaceID: db.Optional[string]{Set: true, V: req.WorkspaceID},
		ExecutionConfig: req.ExecutionConfig, Title: title, Prompt: req.Prompt, Start: true,
	}
	if err := s.chooseConnection(r, &cfg); err != nil {
		return err
	}
	in, err := cfg.validate()
	if err != nil {
		return err
	}
	if err := s.validateTaskDestination(ctx, in); err != nil {
		return err
	}
	result, warnings, err := s.Engine.Coordinator.SetSteerer(ctx, id, db.SteererInput{Task: in, Replace: req.Replace, RequestID: req.RequestID, Digest: digest})
	if err != nil {
		return err
	}
	s.treeChanged()
	task, err := s.Store.GetTask(ctx, result.TaskID)
	if err != nil {
		return err
	}
	if tree, err = s.Store.GetTree(ctx, id); err != nil {
		return err
	}
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}
	shaped := s.task(task)
	writeJSON(w, status, steererOut{Tree: s.treeDetail(tree), Task: &shaped, Warnings: nonNilStrings(warnings)})
	return nil
}

// steererDigest hashes a set-steerer request without its requestId, so a
// replay with other content is told apart.
func steererDigest(req setSteererReq) (string, error) {
	req.RequestID = ""
	raw, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// endSteering ends the tree's steerer binding and stops its session.
func (s *server) endSteering(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	_, warnings, err := s.Engine.Coordinator.EndSteering(ctx, id)
	if err != nil {
		return err
	}
	s.treeChanged()
	tree, err := s.Store.GetTree(ctx, id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, steererOut{Tree: s.treeDetail(tree), Warnings: nonNilStrings(warnings)})
	return nil
}

// Steering feed page sizes.
const (
	steeringEventsDefault = 50
	steeringEventsMax     = 200
)

// steeringEvents: GET /api/trees/{id}/steering/events?after=&limit=, every
// event of the tree after the cursor, ascending, each with self.
func (s *server) steeringEvents(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	after, err := queryInt64(q, "after")
	if err != nil {
		return err
	}
	if after == nil {
		zero := int64(0)
		after = &zero
	}
	if *after < 0 {
		return db.Invalid("after must be 0 or more")
	}
	limit, err := queryInt64(q, "limit")
	if err != nil {
		return err
	}
	n := steeringEventsDefault
	if limit != nil {
		if *limit < 1 || *limit > steeringEventsMax {
			return db.Invalid("limit must be 1 to %d", steeringEventsMax)
		}
		n = int(*limit)
	}
	events, more, err := s.Store.SteeringEvents(r.Context(), id, *after, n)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "hasMore": more})
	return nil
}

// escalateTask: POST /api/tasks/{id}/escalate {reason}. Only the caller
// session of the active steerer of the task's tree (else 403 not_steerer):
// it releases the task's held notification now, or notifies now when the
// task needs input and nothing is held.
func (s *server) escalateTask(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if err := decodeStrict(r, &in); err != nil {
		return err
	}
	caller, _, err := callerSession(r.Context())
	if err != nil {
		return err
	}
	if s.Holds == nil {
		return execution.ErrUnavailable
	}
	if err := s.Holds.Escalate(r.Context(), id, caller.TaskID, in.Reason); err != nil {
		return err
	}
	t, err := s.Store.GetTask(r.Context(), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, s.task(t))
	return nil
}
