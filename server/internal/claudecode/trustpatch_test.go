package claudecode

import (
	"strings"
	"testing"
)

// setTrust changes only the trust key's bytes, laid out like its
// neighbors, and keeps every other byte, key order and spelling.
func TestSetTrustPreservesFormatting(t *testing.T) {
	const dir = "/src/a&b"
	for name, tc := range map[string]struct{ in, want string }{
		"flips false": {
			in:   "{\n  \"numStartups\": 3,\n  \"projects\": {\n    \"/src/a&b\": {\n      \"allowedTools\": [],\n      \"hasTrustDialogAccepted\": false,\n      \"x\": 1e3\n    }\n  },\n  \"z\": \"\\u00e9\"\n}\n",
			want: "{\n  \"numStartups\": 3,\n  \"projects\": {\n    \"/src/a&b\": {\n      \"allowedTools\": [],\n      \"hasTrustDialogAccepted\": true,\n      \"x\": 1e3\n    }\n  },\n  \"z\": \"\\u00e9\"\n}\n",
		},
		"adds the key to an entry": {
			in:   "{\n  \"projects\": {\n    \"/src/a&b\": {\n      \"allowedTools\": [\"Bash\"]\n    }\n  }\n}",
			want: "{\n  \"projects\": {\n    \"/src/a&b\": {\n      \"allowedTools\": [\"Bash\"],\n      \"hasTrustDialogAccepted\": true\n    }\n  }\n}",
		},
		"adds the key to an empty entry": {
			in:   "{\n  \"projects\": {\n    \"/src/a&b\": {}\n  }\n}",
			want: "{\n  \"projects\": {\n    \"/src/a&b\": {\n      \"hasTrustDialogAccepted\": true\n    }\n  }\n}",
		},
		"adds an entry": {
			in:   "{\n  \"projects\": {\n    \"/other\": {\n      \"hasTrustDialogAccepted\": true\n    }\n  },\n  \"theme\": \"dark\"\n}\n",
			want: "{\n  \"projects\": {\n    \"/other\": {\n      \"hasTrustDialogAccepted\": true\n    },\n    \"/src/a&b\": {\n      \"hasTrustDialogAccepted\": true\n    }\n  },\n  \"theme\": \"dark\"\n}\n",
		},
		"adds an entry to empty projects": {
			in:   "{\n  \"projects\": {},\n  \"theme\": \"dark\"\n}",
			want: "{\n  \"projects\": {\n    \"/src/a&b\": {\n      \"hasTrustDialogAccepted\": true\n    }\n  },\n  \"theme\": \"dark\"\n}",
		},
		"adds projects": {
			in:   "{\n\t\"numStartups\": 1\n}",
			want: "{\n\t\"numStartups\": 1,\n\t\"projects\": {\n\t\t\"/src/a&b\": {\n\t\t\t\"hasTrustDialogAccepted\": true\n\t\t}\n\t}\n}",
		},
		"compact": {
			in:   `{"a":[{"b":"}"}],"projects":{"/o":{"hasTrustDialogAccepted":true}}}`,
			want: `{"a":[{"b":"}"}],"projects":{"/o":{"hasTrustDialogAccepted":true},"/src/a&b":{"hasTrustDialogAccepted":true}}}`,
		},
		"compact empty document": {
			in:   `{}`,
			want: `{"projects":{"/src/a&b":{"hasTrustDialogAccepted":true}}}`,
		},
		"last duplicate counts": {
			in:   `{"projects":{"/x":{}},"projects":{"/src/a&b":{"hasTrustDialogAccepted":false}}}`,
			want: `{"projects":{"/x":{}},"projects":{"/src/a&b":{"hasTrustDialogAccepted":true}}}`,
		},
		"escaped key matches": {
			in:   `{"projects":{"\/src\/a\u0026b":{"hasTrustDialogAccepted":0}}}`,
			want: `{"projects":{"\/src\/a\u0026b":{"hasTrustDialogAccepted":true}}}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := setTrust([]byte(tc.in), dir)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("got\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}

func TestSetTrustRefusesUnexpectedShapes(t *testing.T) {
	for name, in := range map[string]string{
		"not json":            `{"projects":`,
		"array":               `[]`,
		"trailing text":       `{} {}`,
		"projects not object": `{"projects":[]}`,
		"entry not object":    `{"projects":{"/d":null}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if out, err := setTrust([]byte(in), "/d"); err == nil {
				t.Fatalf("patched %q to %q", in, out)
			}
		})
	}
	// A key with a quote or backslash is encoded, not spliced raw.
	got, err := setTrust([]byte(`{}`), `/a"b\c`)
	if err != nil || !strings.Contains(string(got), `"/a\"b\\c"`) {
		t.Fatalf("got %s, %v", got, err)
	}
}
