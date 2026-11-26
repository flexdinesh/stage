package claudecode

// Wake marks session id as being delivered to by a Send until the returned
// func runs (tests of the guard without racing a Send).
func (a *Adapter) Wake(id string) func() { return a.wake(id) }
