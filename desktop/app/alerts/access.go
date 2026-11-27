package alerts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"slices"
	"strings"

	"github.com/flexdinesh/stage/desktop/app/target"
)

// Session is the native sign-in for one target. The zero value signs out.
type Session struct {
	// ID is an opaque identifier for this sign-in, stable across app launches
	// and distinct when the credential changes. It is not the credential.
	ID string
	// Prepare is called before each connection. The native credential owner
	// verifies the installation ID for this target and attaches its Stage
	// credential. Return ErrAccessEnded if the installation or session has
	// changed. Other errors use the stream's usual retry policy.
	Prepare func(*http.Request) error
}

func (a Session) scope(t target.Target) string {
	if a.ID == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(t.BaseURL() + "\x00" + t.Socket + "\x00" + a.ID))
	return hex.EncodeToString(digest[:])
}

// SetSession changes the sign-in for the exact saved target and reconnects its
// stream. Restore saved sessions before starting their streams to resume from
// the previous cursor. False means the target or session was invalid.
func (s *Service) SetSession(t target.Target, session Session) bool {
	if (session.ID == "") != (session.Prepare == nil) {
		return false
	}
	s.mu.Lock()
	st := s.streams[t.ID]
	if st == nil || !st.t.Same(t) || s.closed {
		s.mu.Unlock()
		return false
	}
	scope := session.scope(t)
	sp := s.prefs.Servers[t.ID]
	if st.session.ID == session.ID && (sp == nil || sp.SessionScope == scope) {
		s.mu.Unlock()
		return true
	}
	var countChanged, cleared bool
	if st.session.ID != "" || (sp != nil && sp.SessionScope != scope) {
		countChanged, cleared = s.clearAccessLocked(st)
	}
	st.session = session
	st.awaitingAccess = session.ID == ""
	s.reconnectLocked(st)
	setBadge := s.badgeLocked()
	s.mu.Unlock()
	s.accessChanged(countChanged, cleared, setBadge)
	return true
}

func (s *Service) endAccess(st *stream, gen uint64) {
	s.mu.Lock()
	if !st.current(gen) {
		s.mu.Unlock()
		return
	}
	t, sessionID := st.t, st.session.ID
	countChanged, cleared := s.clearAccessLocked(st)
	st.awaitingAccess = true
	setBadge := s.badgeLocked()
	s.mu.Unlock()
	s.accessChanged(countChanged, cleared, setBadge)
	if s.cfg.AccessEnded != nil && sessionID != "" {
		s.cfg.AccessEnded(t, sessionID)
	}
}

func (s *Service) accessChanged(countChanged, cleared, setBadge bool) {
	if cleared {
		s.saveLogged()
	}
	if setBadge {
		s.publishBadge()
	}
	if countChanged {
		s.changed()
	}
}

// clearAccessLocked purges the stream's state. The delivery worker closes
// these IDs before showing another note, even if a group ID is reused.
func (s *Service) clearAccessLocked(st *stream) (countChanged, cleared bool) {
	for id := range st.shown {
		s.pendingCloses[id] = struct{}{}
	}
	if sp := s.prefs.Servers[st.id]; sp != nil {
		for _, id := range sp.Shown {
			if server, _, namespaced := strings.Cut(id, "/"); server == st.id || (!namespaced && st.id == target.LocalID) {
				s.pendingCloses[id] = struct{}{}
			}
		}
		delete(s.prefs.Servers, st.id)
		s.markDirtyLocked()
		cleared = true
	}
	s.resetDeliveryLocked(st)
	st.shown = map[string]shownNote{}
	st.summaryTags, st.unread, st.snapSeq = nil, nil, 0
	st.session = Session{}
	countChanged = st.hasBadge
	st.badge, st.hasBadge = 0, false
	if len(s.pendingCloses) > 0 {
		signal(s.queueWake)
	}
	return countChanged, cleared
}

func (s *Service) resetDeliveryLocked(st *stream) {
	st.cancelDelivery()
	st.delivery, st.cancelDelivery = context.WithCancel(st.ctx)
	s.queue = slices.DeleteFunc(s.queue, func(q queued) bool { return q.server == st.id })
}
