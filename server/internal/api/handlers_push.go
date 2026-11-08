package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/flexdinesh/t3-stage/server/internal/db"
)

// ---- notifications (Web Push) ----

func (s *server) pushConfig(w http.ResponseWriter, r *http.Request) error {
	key, err := s.Push.PublicKey(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"publicKey": key})
	return nil
}

type pushSubscriptionBody struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// endpointArg validates a subscription endpoint (https URL).
func endpointArg(raw string) (string, error) {
	e := strings.TrimSpace(raw)
	if e == "" {
		return "", db.Invalid("endpoint is required")
	}
	if u, err := url.Parse(e); err != nil || u.Scheme != "https" || u.Host == "" {
		return "", db.Invalid("endpoint must be an https URL")
	}
	return e, nil
}

func (s *server) subscribePush(w http.ResponseWriter, r *http.Request) error {
	var b pushSubscriptionBody
	if err := decode(r, &b); err != nil {
		return err
	}
	endpoint, err := endpointArg(b.Endpoint)
	if err != nil {
		return err
	}
	p256dh, auth := strings.TrimSpace(b.Keys.P256dh), strings.TrimSpace(b.Keys.Auth)
	if p256dh == "" || auth == "" {
		return db.Invalid("keys.p256dh and keys.auth are required")
	}
	if err := s.Store.UpsertPushSubscription(r.Context(), endpoint, p256dh, auth, r.UserAgent()); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *server) unsubscribePush(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Endpoint string `json:"endpoint"`
	}
	if err := decode(r, &b); err != nil {
		return err
	}
	endpoint := strings.TrimSpace(b.Endpoint)
	if endpoint == "" {
		return db.Invalid("endpoint is required")
	}
	if err := s.Store.DeletePushSubscription(r.Context(), endpoint); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *server) testPush(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Endpoint string `json:"endpoint"`
	}
	if err := decode(r, &b); err != nil {
		return err
	}
	endpoint := strings.TrimSpace(b.Endpoint)
	if endpoint == "" {
		return db.Invalid("endpoint is required")
	}
	if err := s.Push.SendTest(r.Context(), endpoint); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *server) badge(w http.ResponseWriter, r *http.Request) error {
	n, err := s.Store.NeedsInputCount(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]int{"needsInput": n})
	return nil
}
