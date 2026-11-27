package db

import (
	"context"
	"crypto/rand"
	"errors"

	"github.com/SherClockHolmes/webpush-go"
	"github.com/flexdinesh/stage/server/internal/db/store"
)

type (
	VAPIDKeys        = store.VAPIDKeys
	PushSubscription = store.PushSubscription
)

// VAPIDKeys returns the stored key pair, generating it on first use.
// Concurrent first calls agree (insert-if-absent, then read).
func (s *Store) VAPIDKeys(ctx context.Context) (VAPIDKeys, error) {
	k, err := read(ctx, s, func(tx store.Tx) (VAPIDKeys, error) { return tx.Push().VAPIDKeys(ctx) })
	if err == nil || !errors.Is(err, ErrNotFound) {
		return k, err
	}
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return k, err
	}
	return write(ctx, s, func(tx store.Tx) (VAPIDKeys, error) {
		if err := tx.Push().InsertVAPIDKeys(ctx, VAPIDKeys{PublicKey: pub, PrivateKey: priv}); err != nil {
			return VAPIDKeys{}, err
		}
		return tx.Push().VAPIDKeys(ctx)
	})
}

// UpsertPushSubscription inserts or refreshes (keys, user agent) by endpoint.
func (s *Store) UpsertPushSubscription(ctx context.Context, sessionID int64, endpoint, p256dh, auth, userAgent string) error {
	sub := PushSubscription{SessionID: sessionID, Endpoint: endpoint, P256dh: p256dh, Auth: auth, UserAgent: userAgent}
	if sessionID != 0 {
		sub.Binding = rand.Text()
	}
	return s.e.Write(ctx, func(tx store.Tx) error {
		return tx.Push().UpsertSubscription(ctx, sub)
	})
}

// DeletePushSubscription removes a subscription (no error when unknown).
func (s *Store) DeletePushSubscription(ctx context.Context, sessionID int64, endpoint string) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Push().DeleteSubscription(ctx, sessionID, endpoint) })
}

// GetPushSubscription returns ErrNotFound for an unknown endpoint.
func (s *Store) GetPushSubscription(ctx context.Context, sessionID int64, endpoint string) (PushSubscription, error) {
	return read(ctx, s, func(tx store.Tx) (PushSubscription, error) {
		return tx.Push().GetSubscription(ctx, sessionID, endpoint)
	})
}

// PushDelivery resolves a reference against the current browser session and
// its inbox. Event zero is an explicit test notification.
func (s *Store) PushDelivery(ctx context.Context, sessionID int64, binding string, eventID int64) (n Notification, badge int, err error) {
	err = s.e.Read(ctx, func(tx store.Tx) error {
		sub, err := tx.Push().GetBinding(ctx, sessionID, binding)
		if err != nil {
			return err
		}
		if sub.UserID != ActingUser(ctx) {
			return ErrNotFound
		}
		if eventID != 0 {
			n, err = tx.Notifications().ForDelivery(ctx, sub.UserID, eventID)
			if err != nil {
				return err
			}
		}
		badge, err = tx.Notifications().UnreadCount(ctx, sub.UserID)
		return err
	})
	return
}

func (s *Store) ListPushSubscriptions(ctx context.Context) ([]PushSubscription, error) {
	return read(ctx, s, func(tx store.Tx) ([]PushSubscription, error) { return tx.Push().ListSubscriptions(ctx) })
}

func (s *Store) PruneInactivePushBindings(ctx context.Context) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Push().PruneInactiveBindings(ctx) })
}

// RecordPushResult stores a send outcome: errMsg nil = success.
func (s *Store) RecordPushResult(ctx context.Context, sub PushSubscription, errMsg *string) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Push().RecordResult(ctx, sub, errMsg) })
}

func (s *Store) DeleteExpiredPushSubscription(ctx context.Context, sub PushSubscription) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Push().DeleteExpired(ctx, sub) })
}
