package api

import (
	"net/http"

	"github.com/flexdinesh/stage/server/internal/db"
)

// me returns the acting user (docs/api.md "Users"): the default user until
// sign-in sets a request principal.
func (s *server) me(w http.ResponseWriter, r *http.Request) error {
	u, err := s.Store.GetUser(r.Context(), db.ActingUser(r.Context()))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, u)
	return nil
}
