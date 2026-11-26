package opencodesetup

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/opencode"
)

// OpenCode authentication modes (SetupSettings.Auth).
const (
	AuthNone   = "none"   // no credentials
	AuthLocal  = "local"  // the password in the local service's registration file (never stored)
	AuthPaired = "paired" // a renewable session token from an `opencode pair` link
	AuthBasic  = "basic"  // entered HTTP Basic credentials
)

type settings struct {
	SetupSettings
	Credential
}

type Edit struct {
	Endpoint        *string `json:"endpoint"`
	BrowserEndpoint *string `json:"browserEndpoint"`
	WebClient       *string `json:"webClient"`
	Username        *string `json:"username"`
	Password        *string `json:"password"`
	// NewInstance confirms connecting a different OpenCode service: the
	// current connection retires and keeps its tasks and bindings.
	NewInstance bool `json:"newInstance"`
}

type Status struct {
	ConnectionID    int64      `json:"connectionId"`
	Endpoint        string     `json:"endpoint"`
	BrowserEndpoint string     `json:"browserEndpoint"`
	WebClient       string     `json:"webClient"`
	HasCredentials  bool       `json:"hasCredentials"`
	State           string     `json:"state"`
	Reason          string     `json:"reason,omitempty"`
	CheckedAt       *time.Time `json:"checkedAt,omitempty"`
	Auth            string     `json:"auth"`
	// ExpiresAt is when a paired session token expires (paired only).
	ExpiresAt *time.Time  `json:"expiresAt,omitempty"`
	Local     LocalStatus `json:"local"`
}

// LocalStatus describes the OpenCode service registered on the Stage
// server's machine. Reason is "" when available, else missing, invalid,
// unreachable or environment.
type LocalStatus struct {
	Available bool   `json:"available"`
	URL       string `json:"url"`
	Version   string `json:"version"`
	Reason    string `json:"reason"`
}

// SetupSettings is the non-secret part of a machine's saved OpenCode
// settings: the main server may hold it.
type SetupSettings struct {
	Endpoint        string `json:"endpoint"`
	BrowserEndpoint string `json:"browserEndpoint"`
	WebClient       string `json:"webClient"`
	Auth            string `json:"auth"`
}

// Credential is the secret part of a machine's saved OpenCode
// settings: HTTP Basic credentials or a paired session token and when it
// was issued. It stays where the harness runs (ADR 0002).
type Credential struct {
	Username      string     `json:"username"`
	Password      string     `json:"password"`
	TokenIssuedAt *time.Time `json:"tokenIssuedAt,omitempty"`
}

func (s settings) transport() opencode.Settings {
	return opencode.Settings{
		Endpoint: s.Endpoint, BrowserEndpoint: s.BrowserEndpoint, WebClient: s.WebClient,
		Username: s.Username, Password: s.Password, Local: s.Auth == AuthLocal,
	}
}

// settingsStore binds the generic ports to OpenCode's public and secret schemas.
type settingsStore struct {
	public harness.Settings
	secret harness.Secrets
	atomic func(context.Context, string, json.RawMessage, string, json.RawMessage) error
}

func (s settingsStore) load(ctx context.Context) (settings, error) {
	var out settings
	raw, err := s.public.Get(ctx, opencode.Kind)
	if err != nil {
		return out, err
	}
	if out.SetupSettings, err = decodeSettings(raw); err != nil {
		return out, err
	}
	raw, err = s.secret.Get(ctx, opencode.Kind, credentialKey)
	if err != nil {
		return out, errors.New("read OpenCode credential")
	}
	if raw != nil {
		out.Credential, err = decodeCredential(raw)
	}
	return out, err
}

func (s settingsStore) save(ctx context.Context, previous, next settings) error {
	patch, err := json.Marshal(next.SetupSettings)
	if err != nil {
		return err
	}
	var value json.RawMessage
	if next.Credential != (Credential{}) {
		value, err = json.Marshal(next.Credential)
		if err != nil {
			return errors.New("encode OpenCode credential")
		}
	}
	if s.atomic != nil {
		if err := s.atomic(ctx, opencode.Kind, patch, credentialKey, value); err != nil {
			return errors.New("save OpenCode settings and credential")
		}
		return nil
	}
	if previous.SetupSettings == next.SetupSettings {
		return s.writeCredential(ctx, value)
	}

	// A worker writes to two stores. Clear the previous credential before
	// publishing settings that could make it apply to a different service.
	clear := previous.Credential != (Credential{}) && (previous.Endpoint != next.Endpoint || previous.Auth != next.Auth)
	if clear {
		if err := s.writeCredential(ctx, nil); err != nil {
			return err
		}
	}
	if err := s.public.Save(ctx, opencode.Kind, patch); err != nil {
		if clear {
			return errors.Join(err, s.restoreCredential(ctx, previous))
		}
		return err
	}
	if err := s.writeCredential(ctx, value); err != nil {
		rollback, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		prior, _ := json.Marshal(previous.SetupSettings)
		if e := s.public.Save(rollback, opencode.Kind, prior); e != nil {
			return errors.Join(err, errors.New("restore OpenCode settings"))
		}
		if clear {
			return errors.Join(err, s.restoreCredential(rollback, previous))
		}
		return err
	}
	return nil
}

func (s settingsStore) writeCredential(ctx context.Context, value json.RawMessage) error {
	var err error
	if value == nil {
		err = s.secret.Delete(ctx, opencode.Kind, credentialKey)
	} else {
		err = s.secret.Set(ctx, opencode.Kind, credentialKey, value)
	}
	if err != nil {
		return errors.New("save OpenCode credential")
	}
	return nil
}

// A failed worker request can have reached the main server. Only restore a
// credential if the saved settings still refer to its original service.
func (s settingsStore) restoreCredential(ctx context.Context, previous settings) error {
	check, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	raw, err := s.public.Get(check, opencode.Kind)
	setup, decodeErr := decodeSettings(raw)
	if err != nil || decodeErr != nil || setup.Endpoint != previous.Endpoint || setup.Auth != previous.Auth {
		return errors.New("could not confirm settings for the previous OpenCode credential")
	}
	value, err := json.Marshal(previous.Credential)
	if err != nil {
		return errors.New("encode previous OpenCode credential")
	}
	return s.writeCredential(check, value)
}
