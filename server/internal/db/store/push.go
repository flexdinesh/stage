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
	UserID        int64
	SessionID     int64
	Binding       string
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
	// UpsertSubscription inserts or refreshes a device for an active browser
	// session (zero for personal mode). Matching keys prove possession when
	// the same browser subscription is rebound to another session.
	UpsertSubscription(ctx context.Context, sub PushSubscription) error
	DeleteSubscription(ctx context.Context, sessionID int64, endpoint string) error
	GetSubscription(ctx context.Context, sessionID int64, endpoint string) (PushSubscription, error)
	GetBinding(ctx context.Context, sessionID int64, binding string) (PushSubscription, error)
	ListSubscriptions(ctx context.Context) ([]PushSubscription, error)
	PruneInactiveBindings(ctx context.Context) error
	// These use the original binding so a delayed response cannot update a
	// device that has since signed in with a different credential.
	DeleteExpired(ctx context.Context, sub PushSubscription) error
	RecordResult(ctx context.Context, sub PushSubscription, errMsg *string) error
}
