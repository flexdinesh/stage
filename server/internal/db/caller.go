package db

import "context"

// CallerSession is the agent session that named itself on a request
// (Stage-Caller-Session), resolved once by the API layer.
type CallerSession struct {
	Ref SessionRef
	// TaskID owns Ref (0: no task owns it).
	TaskID int64
}

type callerSessionKey struct{}

// WithCallerSession returns ctx carrying the request's caller session.
func WithCallerSession(ctx context.Context, c CallerSession) context.Context {
	return context.WithValue(ctx, callerSessionKey{}, c)
}

// CallerSessionFrom returns the request's caller session (false: none named).
func CallerSessionFrom(ctx context.Context) (CallerSession, bool) {
	c, ok := ctx.Value(callerSessionKey{}).(CallerSession)
	return c, ok
}

// CallerRef is the caller session's ref (nil: none named).
func CallerRef(ctx context.Context) *SessionRef {
	c, ok := CallerSessionFrom(ctx)
	if !ok {
		return nil
	}
	ref := c.Ref
	return &ref
}
