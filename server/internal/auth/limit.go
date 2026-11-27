package auth

import (
	"crypto/sha256"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

const (
	limitWindow   = 5 * time.Minute
	accountLimit  = 10
	ipLimit       = 40
	limitCapacity = 4096
)

type attempts struct {
	count int
	until time.Time
}

type loginLimiter struct {
	mu        sync.Mutex
	accounts  map[[32]byte]attempts
	addresses map[netip.Addr]attempts
	sweepAt   time.Time
}

func (l *loginLimiter) allow(email string, ip netip.Addr, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !now.Before(l.sweepAt) {
		sweep(l.accounts, now)
		sweep(l.addresses, now)
		l.sweepAt = now.Add(time.Minute)
	}
	// A Unix or unavailable peer uses the same bounded fallback bucket.
	// The transport supplies the verified peer address.
	if !take(l.addresses, ip.Unmap().WithZone(""), ipLimit, now) {
		return false
	}
	email = strings.TrimSpace(email)
	if len(email) > emailMax {
		email = ""
	}
	key := sha256.Sum256([]byte(store.EmailKey(email)))
	return take(l.accounts, key, accountLimit, now)
}

func take[K comparable](buckets map[K]attempts, key K, limit int, now time.Time) bool {
	bucket, exists := buckets[key]
	if !exists && len(buckets) >= limitCapacity {
		return false
	}
	if !now.Before(bucket.until) {
		bucket = attempts{until: now.Add(limitWindow)}
	}
	if bucket.count >= limit {
		return false
	}
	bucket.count++
	buckets[key] = bucket
	return true
}

func sweep[K comparable](buckets map[K]attempts, now time.Time) {
	for key, bucket := range buckets {
		if !now.Before(bucket.until) {
			delete(buckets, key)
		}
	}
}
