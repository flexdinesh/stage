package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
)

// listTrees: GET /api/trees?status=&all=1&completedFrom=&project= (docs/api.md
// "Trees"): status active (default; plus open trees completed since
// completedFrom) | done | archived | all; all=1 is status=all.
func (s *server) listTrees(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	var f db.TreeFilter
	var err error
	switch v := strings.TrimSpace(q.Get("all")); v {
	case "", "0", "false":
	case "1", "true":
		f.View = db.TreeViewAll
	default:
		return db.Invalid("invalid all %q (want 1|0)", v)
	}
	if v := strings.TrimSpace(q.Get("status")); v != "" {
		if err := validEnum("status", v, db.TreeViews); err != nil {
			return err
		}
		f.View = db.TreeView(v)
	}
	if f.CompletedFrom, err = queryCompletedFrom(q); err != nil {
		return err
	}
	if f.ProjectID, err = queryInt64(q, "project"); err != nil {
		return err
	}
	trees, err := s.Store.ListTrees(r.Context(), f)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"trees": trees})
	return nil
}

// createTree: POST /api/trees {title, startMode?, tasks: [{taskId, needs?}]}.
func (s *server) createTree(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Title     string          `json:"title"`
		StartMode *db.StartMode   `json:"startMode"`
		Tasks     []db.TreeMember `json:"tasks"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	in := db.NewTree{Title: strings.TrimSpace(req.Title), StartMode: db.StartAuto, Tasks: req.Tasks}
	if in.Title == "" {
		return db.Invalid("title is required")
	}
	if req.StartMode != nil {
		if !req.StartMode.Valid() {
			return db.Invalid("invalid startMode %q (want auto|manual)", *req.StartMode)
		}
		in.StartMode = *req.StartMode
	}
	tree, err := s.Store.CreateTree(r.Context(), in)
	if err != nil {
		return err
	}
	s.treeChanged()
	writeJSON(w, http.StatusCreated, s.treeDetail(tree))
	return nil
}

func (s *server) getTree(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	return s.writeTree(w, r, http.StatusOK, id)
}

// writeTree writes tree id's detail.
func (s *server) writeTree(w http.ResponseWriter, r *http.Request, status int, id int64) error {
	tree, err := s.Store.GetTree(r.Context(), id)
	if err != nil {
		return err
	}
	writeJSON(w, status, s.treeDetail(tree))
	return nil
}

// updateTree: PATCH /api/trees/{id} {title?, startMode?, status?} (status
// first: open reopens, done marks done, archived archives).
func (s *server) updateTree(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var p db.TreePatch
	if err := decode(r, &p); err != nil {
		return err
	}
	if err := requiredText("title", &p.Title); err != nil {
		return err
	}
	if p.StartMode.Set && (p.StartMode.Null || !p.StartMode.V.Valid()) {
		return db.Invalid("startMode must be auto or manual")
	}
	if p.Status.Set {
		if p.Status.Null || !p.Status.V.Valid() {
			return db.Invalid("status must be open, done or archived")
		}
		if _, err := s.Store.SetTreeStatus(r.Context(), id, p.Status.V); err != nil {
			return err
		}
	}
	if _, err := s.Store.UpdateTree(r.Context(), id, p); err != nil {
		return err
	}
	s.treeChanged()
	return s.writeTree(w, r, http.StatusOK, id)
}

// deleteTree: DELETE /api/trees/{id} deletes an unstarted tree and its
// tasks (409 once a member started: archive it).
func (s *server) deleteTree(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteTree(r.Context(), id); err != nil {
		return err
	}
	s.treeChanged()
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// dissolveTree: POST /api/trees/{id}/dissolve archives the tree; its tasks
// stay (standalone), their needs go.
func (s *server) dissolveTree(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if err := s.Store.DissolveTree(r.Context(), id); err != nil {
		return err
	}
	s.treeChanged()
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// addToTree: POST /api/trees/{id}/tasks {taskId, needs?} (needs omitted =
// keep the task's needs).
func (s *server) addToTree(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var req struct {
		TaskID *int64   `json:"taskId"`
		Needs  *[]int64 `json:"needs"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.TaskID == nil {
		return db.Invalid("taskId is required")
	}
	var needs []int64
	if req.Needs != nil {
		needs = append([]int64{}, *req.Needs...)
	}
	tree, err := s.Store.AddToTree(r.Context(), id, *req.TaskID, needs)
	if err != nil {
		return err
	}
	s.treeChanged()
	writeJSON(w, http.StatusOK, s.treeDetail(tree))
	return nil
}

// removeFromTree: DELETE /api/trees/{id}/tasks/{taskId} splices the task out.
func (s *server) removeFromTree(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	taskID, err := strconv.ParseInt(r.PathValue("taskId"), 10, 64)
	if err != nil || taskID <= 0 {
		return db.Invalid("invalid taskId %q", r.PathValue("taskId"))
	}
	if err := s.Store.RemoveFromTree(r.Context(), id, taskID); err != nil {
		return err
	}
	s.treeChanged()
	w.WriteHeader(http.StatusNoContent)
	return nil
}

type treeStartError struct {
	TaskID int64  `json:"taskId"`
	Error  string `json:"error"`
}

// startTree: POST /api/trees/{id}/start starts every ready member ("Start
// ready") -> {started: Task[], errors: [{taskId, error}]}.
func (s *server) startTree(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	ids, err := s.Store.StartableInTree(ctx, id)
	if err != nil {
		return err
	}
	started, failed := []taskJSON{}, []treeStartError{}
	for _, taskID := range ids {
		task, err := s.Sync.StartTask(ctx, taskID)
		if err != nil {
			failed = append(failed, treeStartError{TaskID: taskID, Error: err.Error()})
			continue
		}
		started = append(started, s.task(task))
	}
	writeJSON(w, http.StatusOK, map[string]any{"started": started, "errors": failed})
	return nil
}
