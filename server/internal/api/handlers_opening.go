package api

import (
	"errors"
	"net/http"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// Only an adopted task's opening prompt can be read or expanded. There is
// no arbitrary item ID, transcript paging or live conversation stream.
func (s *server) getOpeningPrompt(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	task, err := s.Store.GetTask(r.Context(), id)
	if err != nil {
		return err
	}
	if !task.Adopted || task.Session == nil {
		return db.Invalid("opening prompt requires an adopted task")
	}
	adapter, err := s.Engine.Connections.Adapter(task.ConnectionID)
	if err != nil {
		return err
	}
	item, err := adapter.Opening(r.Context(), *task.Session, r.URL.Query().Get("full") == "true")
	if errors.Is(err, execution.ErrNotFound) {
		return db.ErrNotFound
	}
	if err != nil {
		return &db.HarnessError{Err: err}
	}
	var result any
	if item != nil {
		result = struct {
			*execution.Reply
			Kind      string `json:"kind"`
			Inherited bool   `json:"inherited"`
		}{item, "user", false}
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": result})
	return nil
}
