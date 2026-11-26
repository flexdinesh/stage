package execution

import (
	"reflect"
	"testing"
)

func TestOptionalTraitRegistrationIsUnique(t *testing.T) {
	names, types := map[string]bool{}, map[reflect.Type]bool{}
	for _, trait := range optionalTraits {
		if trait.name == "" || names[trait.name] || types[trait.typ] || trait.typ.Kind() != reflect.Interface {
			t.Fatalf("invalid or duplicate optional trait %q for %v", trait.name, trait.typ)
		}
		names[trait.name], types[trait.typ] = true, true
	}
}

// OptionalInterfaces names registered interfaces for the As boundary check.
func OptionalInterfaces() map[string]bool {
	out := make(map[string]bool, len(optionalTraits))
	for _, trait := range optionalTraits {
		out[trait.typ.Name()] = true
	}
	return out
}
