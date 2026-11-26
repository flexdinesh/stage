package claudecodesetup

import (
	"context"
	"path/filepath"

	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// Module composes Claude Code on the server or a worker.
var Module = harness.Module{
	Descriptor:       claudecode.Descriptor,
	Compose:          compose,
	ValidateSettings: validateSettings,
	Owners:           []string{"internal/claudecode/", "internal/claudecodesetup/"},
}

func compose(ctx context.Context, p harness.Ports) (harness.Composition, error) {
	var o Options
	switch override := p.Overrides[claudecode.Kind].(type) {
	case Options:
		o = override
	case *Options:
		if override != nil {
			o = *override
		} else {
			o.Scope = claudecode.ScopeAvailable()
		}
	default:
		o.Scope = claudecode.ScopeAvailable()
	}
	if o.LookPath == nil {
		o.LookPath = p.LookupPath
	}
	if o.StateDir == "" && p.DataDir != "" {
		o.StateDir = filepath.Join(p.DataDir, "claude")
	}
	o.Logger = p.Logger
	svc, err := New(ctx, p, o)
	if err != nil {
		return harness.Composition{}, err
	}
	return harness.Composition{Connector: NewConnector(svc), Close: svc.Close}, nil
}
