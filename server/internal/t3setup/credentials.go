package t3setup

import (
	"context"
	"encoding/json"

	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/t3"
)

const (
	tokenKey      = "token"
	adminTokenKey = "adminToken"
)

type credentials struct {
	secrets      harness.Secrets
	setLastError func(context.Context, string, string) error
}

var _ t3.TokenStore = credentials{}

func (c credentials) LoadToken(ctx context.Context) (t3.StoredToken, error) {
	return loadSecret[t3.StoredToken](ctx, c.secrets, tokenKey)
}

func (c credentials) SaveToken(ctx context.Context, tok t3.StoredToken) error {
	if tok == (t3.StoredToken{}) {
		return c.secrets.Delete(ctx, t3.Kind, tokenKey)
	}
	return saveSecret(ctx, c.secrets, tokenKey, tok)
}

func (c credentials) SetLastError(ctx context.Context, msg string) error {
	if c.setLastError == nil {
		return nil
	}
	return c.setLastError(ctx, t3.Kind, msg)
}

func loadSecret[T any](ctx context.Context, secrets harness.Secrets, key string) (T, error) {
	var value T
	raw, err := secrets.Get(ctx, t3.Kind, key)
	if err != nil || len(raw) == 0 {
		return value, err
	}
	err = json.Unmarshal(raw, &value)
	return value, err
}

func saveSecret(ctx context.Context, secrets harness.Secrets, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return secrets.Set(ctx, t3.Kind, key, raw)
}

func validateSecret[T any](raw json.RawMessage) error {
	var value T
	return json.Unmarshal(raw, &value)
}
