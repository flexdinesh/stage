package execution

import "context"

// SessionNamer renames one of its connection's sessions in the harness
// (operation sessionRename). Stage calls it after a task's title changes,
// only for a session Stage launched. The harness owns the name; a rename
// changes nothing else.
type SessionNamer interface {
	RenameSession(ctx context.Context, ref SessionRef, title string) error
}
