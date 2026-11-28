package execution_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/flexdinesh/stage/server/internal/execution"
)

func TestListingFiltersSortsAndBounds(t *testing.T) {
	entries := []execution.DirEntry{
		{Name: "src", Path: "/w/src"}, {Name: ".git", Path: "/w/.git"}, {Name: "Docs", Path: "/w/Docs"},
		{Name: "api", Path: "/w/api"}, {Name: "", Path: "/w"}, {Name: "..", Path: "/"},
	}
	got := execution.Listing("/w/", entries, false)
	if got.Path != "/w" || got.Parent != "/" || got.Truncated {
		t.Fatalf("listing %+v", got)
	}
	names := func(r execution.BrowseResult) []string {
		out := []string{}
		for _, e := range r.Entries {
			out = append(out, e.Name)
		}
		return out
	}
	if want := []string{"api", "Docs", "src"}; !slices.Equal(names(got), want) {
		t.Fatalf("names %v, want %v", names(got), want)
	}
	if want := []string{".git", "api", "Docs", "src"}; !slices.Equal(names(execution.Listing("/w", entries, true)), want) {
		t.Fatalf("hidden names %v", names(execution.Listing("/w", entries, true)))
	}

	many := make([]execution.DirEntry, execution.MaxDirectoryEntries+3)
	for i := range many {
		many[i] = execution.DirEntry{Name: fmt.Sprintf("d%04d", i), Path: fmt.Sprintf("/big/d%04d", i)}
	}
	big := execution.Listing("/big", many, false)
	if len(big.Entries) != execution.MaxDirectoryEntries || !big.Truncated || big.Entries[0].Name != "d0000" {
		t.Fatalf("big: %d truncated=%v", len(big.Entries), big.Truncated)
	}
	if empty := execution.Listing("/", nil, false); empty.Parent != "" || empty.Entries == nil {
		t.Fatalf("root %+v", empty)
	}
}

func TestParentDir(t *testing.T) {
	for in, want := range map[string]string{"/": "", "/a": "/", "/a/b/": "/a", "a/b": "", "": ""} {
		if got := execution.ParentDir(in); got != want {
			t.Errorf("ParentDir(%q) = %q, want %q", in, got, want)
		}
	}
}

type browsing struct{ plain }

func (browsing) Browse(context.Context, execution.BrowseRequest) (execution.BrowseResult, error) {
	return execution.BrowseResult{}, nil
}

func TestDirectoryBrowserTrait(t *testing.T) {
	if !slices.Contains(execution.Ops, execution.OpDirectoryBrowse) {
		t.Fatalf("ops %v", execution.Ops)
	}
	trait := execution.TraitOf[execution.DirectoryBrowser]()
	if trait != "directoryBrowser" {
		t.Fatalf("trait %q", trait)
	}
	if _, ok := execution.As[execution.DirectoryBrowser](browsing{}); !ok {
		t.Fatal("browser not found")
	}
	if got := execution.Traits(browsing{}); !slices.Contains(got, trait) {
		t.Fatalf("traits %v", got)
	}
	if _, ok := execution.As[execution.DirectoryBrowser](plain{}); ok {
		t.Fatal("plain adapter browses")
	}
}
