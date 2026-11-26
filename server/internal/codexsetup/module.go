package codexsetup

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/codex"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// Module composes Codex on the server or a worker.
var Module = harness.Module{
	Descriptor:       codex.Descriptor,
	Compose:          compose,
	ValidateSettings: validateSettings,
	Owners:           []string{"internal/codex/", "internal/codexsetup/"},
}

func compose(ctx context.Context, p harness.Ports) (harness.Composition, error) {
	var o Options
	switch override := p.Overrides[codex.Kind].(type) {
	case Options:
		o = override
	case *Options:
		if override != nil {
			o = *override
		}
	}
	o.Ports, o.Logger = p, p.Logger
	if o.Version == "" {
		o.Version = buildinfo.Version
	}
	svc, err := New(ctx, o)
	if err != nil {
		return harness.Composition{}, err
	}
	return harness.Composition{Connector: NewConnector(svc), Close: svc.Close}, nil
}
