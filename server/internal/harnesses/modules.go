// Package harnesses is the registration point for the harnesses Stage ships.
package harnesses

import (
	"slices"

	"github.com/flexdinesh/stage/server/internal/claudecodesetup"
	"github.com/flexdinesh/stage/server/internal/codexsetup"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/opencodesetup"
	"github.com/flexdinesh/stage/server/internal/t3setup"
)

var modules = []harness.Module{
	t3setup.Module,
	opencodesetup.Module,
	claudecodesetup.Module,
	codexsetup.Module,
}

func Modules() []harness.Module { return slices.Clone(modules) }

func Registry() *harness.Registry {
	descriptors := make([]harness.Descriptor, 0, len(modules))
	for _, m := range modules {
		descriptors = append(descriptors, m.Descriptor)
	}
	return harness.MustRegistry(descriptors...)
}

func Env() harness.EnvOptions {
	var out harness.EnvOptions
	for _, m := range modules {
		out.Keys = append(out.Keys, m.Env.Keys...)
		out.Removed = append(out.Removed, m.Env.Removed...)
		out.WorkerTemplate += m.Env.WorkerTemplate
	}
	return out
}

func LegacySecrets() []harness.LegacySecret {
	var out []harness.LegacySecret
	for _, m := range modules {
		out = append(out, m.LegacySecrets...)
	}
	return out
}
