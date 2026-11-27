package api

import (
	"net/http"

	"github.com/flexdinesh/stage/server/internal/db"
)

// me returns the caller's profile. Other people's work uses the public
// display-name projection on actor records.
func (s *server) me(w http.ResponseWriter, r *http.Request) error {
	if c, ok := credentialFrom(r.Context()); ok {
		writeJSON(w, http.StatusOK, viewAccount(c.identity.Account))
		return nil
	}
	u, err := s.Store.GetUser(r.Context(), db.ActingUser(r.Context()))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, u)
	return nil
}
