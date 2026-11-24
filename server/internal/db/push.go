package db

import (
	"context"
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
func (s *Store) UpsertPushSubscription(ctx context.Context, endpoint, p256dh, auth, userAgent string) error {
	return s.e.Write(ctx, func(tx store.Tx) error {
		return tx.Push().UpsertSubscription(ctx, endpoint, p256dh, auth, userAgent)
	})
}

// DeletePushSubscription removes a subscription (no error when unknown).
func (s *Store) DeletePushSubscription(ctx context.Context, endpoint string) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Push().DeleteSubscription(ctx, endpoint) })
}

// GetPushSubscription returns ErrNotFound for an unknown endpoint.
func (s *Store) GetPushSubscription(ctx context.Context, endpoint string) (PushSubscription, error) {
	return read(ctx, s, func(tx store.Tx) (PushSubscription, error) { return tx.Push().GetSubscription(ctx, endpoint) })
}

func (s *Store) ListPushSubscriptions(ctx context.Context) ([]PushSubscription, error) {
	return read(ctx, s, func(tx store.Tx) ([]PushSubscription, error) { return tx.Push().ListSubscriptions(ctx) })
}

// RecordPushResult stores a send outcome: errMsg nil = success.
func (s *Store) RecordPushResult(ctx context.Context, endpoint string, errMsg *string) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Push().RecordResult(ctx, endpoint, errMsg) })
}
