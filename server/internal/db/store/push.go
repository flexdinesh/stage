package store

import (
	"context"
	"time"
)

// VAPIDKeys is the server's Web Push key pair (base64url).
type VAPIDKeys struct {
	PublicKey  string
	PrivateKey string
}

// PushSubscription is a browser Web Push subscription.
type PushSubscription struct {
	ID            int64
	Endpoint      string
	P256dh        string
	Auth          string
	UserAgent     string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	LastSuccessAt *time.Time
	LastError     *string
}

// Push is the Web Push repository: the VAPID key pair and subscriptions.
type Push interface {
	// VAPIDKeys reads the stored pair (ErrNotFound when none).
	VAPIDKeys(ctx context.Context) (VAPIDKeys, error)
	// InsertVAPIDKeys stores k unless a pair exists.
	InsertVAPIDKeys(ctx context.Context, k VAPIDKeys) error
	// UpsertSubscription inserts or refreshes (keys, user agent) by endpoint.
	UpsertSubscription(ctx context.Context, endpoint, p256dh, auth, userAgent string) error
	// DeleteSubscription removes a subscription (no error when unknown).
	DeleteSubscription(ctx context.Context, endpoint string) error
	// GetSubscription returns ErrNotFound for an unknown endpoint.
	GetSubscription(ctx context.Context, endpoint string) (PushSubscription, error)
	ListSubscriptions(ctx context.Context) ([]PushSubscription, error)
	// RecordResult stores a send outcome: errMsg nil = success.
	RecordResult(ctx context.Context, endpoint string, errMsg *string) error
}
