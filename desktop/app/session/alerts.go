package session

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/flexdinesh/stage/desktop/app/alerts"
	"github.com/flexdinesh/stage/desktop/app/target"
)

// AlertSession supplies the exact sign-in that owns a server's native
// notification cursor, including after a desktop restart.
func (s *Store) AlertSession(t target.Target) alerts.Session {
	c := s.current(t)
	if c.Value == "" || c.Binding.Endpoint != endpoint(t) {
		return alerts.Session{}
	}
	return alerts.Session{ID: c.Scope, Prepare: func(r *http.Request) error {
		if c.ctx.Err() != nil {
			return alerts.ErrAccessEnded
		}
		tr := t.Transport()
		defer tr.CloseIdleConnections()
		key, err := resolve(r.Context(), t, tr)
		var inaccessible *targetAccessError
		if errors.As(err, &inaccessible) || (err == nil && key != c.Binding) {
			return alerts.ErrAccessEnded
		}
		if err != nil {
			return err
		}
		if c.ctx.Err() != nil {
			return alerts.ErrAccessEnded
		}
		u, _ := url.Parse(key.Canonical)
		r.Host = u.Host
		r.AddCookie(&http.Cookie{Name: cookieName, Value: c.Value})
		ctx, cancel := context.WithCancel(r.Context())
		stop := context.AfterFunc(c.ctx, cancel)
		context.AfterFunc(r.Context(), func() { stop(); cancel() })
		*r = *r.WithContext(ctx)
		return nil
	}}
}

// AlertAccessEnded discards only the sign-in rejected by this stream.
func (s *Store) AlertAccessEnded(t target.Target, id string) error {
	c := s.current(t)
	if c.Scope != id || c.Binding.Endpoint != endpoint(t) {
		return nil
	}
	err := s.replace(context.Background(), t, c, credential{})
	if errors.Is(err, errChanged) {
		return nil
	}
	return err
}
