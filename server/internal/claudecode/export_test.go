package claudecode

// Wake marks session id as being delivered to by a Send until the returned
// func runs (tests of the guard without racing a Send).
func (a *Adapter) Wake(id string) func() { return a.wake(id) }

func (a *Adapter) HasHookSubscriber() bool {
	a.hooks.mu.Lock()
	defer a.hooks.mu.Unlock()
	return a.hooks.sub != nil
}

// BeforeTrustRename runs f before each trust write's final check (tests of
// a concurrent writer).
func (a *Adapter) BeforeTrustRename(f func()) { a.beforeRename = f }
