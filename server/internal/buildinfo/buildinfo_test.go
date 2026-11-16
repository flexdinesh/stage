package buildinfo

import "testing"

func TestLabelAndIdentity(t *testing.T) {
	defer func(id, l string) { Identity, ServiceLabel = id, l }(Identity, ServiceLabel)
	Identity, ServiceLabel = "stage-dev", ""
	if Label() != "io.github.flexdinesh.stage-dev" || !ValidIdentity() {
		t.Fatalf("derived label %q valid %v", Label(), ValidIdentity())
	}
	ServiceLabel = "com.example.stage"
	if Label() != "com.example.stage" {
		t.Fatalf("override %q", Label())
	}
	for _, bad := range []string{"", "Stage", "-x", "a b", "a/b"} {
		Identity = bad
		if ValidIdentity() {
			t.Fatalf("%q valid", bad)
		}
	}
}
