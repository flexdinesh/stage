package api

import (
	"errors"
	"net/http"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/opening"
	"github.com/flexdinesh/stage/server/internal/t3"
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
	if !task.Adopted || task.T3ThreadID == nil {
		return db.Invalid("opening prompt requires an adopted task")
	}
	reader := opening.Reader{T3: s.T3}
	item, err := reader.Opening(r.Context(), *task.T3ThreadID)
	if err == nil && item != nil && r.URL.Query().Get("full") == "true" && item.Truncated {
		item.Text, item.Truncated, err = reader.FullText(r.Context(), *task.T3ThreadID, item.ItemID)
	}
	if errors.Is(err, t3.ErrNotFound) {
		return db.ErrNotFound
	}
	if err != nil {
		return &db.T3Error{Err: err}
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": item})
	return nil
}
