package db

import (
	"crypto/sha256"
	"strings"
	"testing"
)

func TestSecretMatches(t *testing.T) {
	secret, hash, err := newSecret(tokenSecretBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(hash) != sha256.Size || secret != strings.ToLower(secret) || len(secret) != 52 {
		t.Fatalf("secret %q hash %d bytes", secret, len(hash))
	}
	for name, tc := range map[string]struct {
		stored []byte
		secret string
		want   bool
	}{
		"equal":                {hash, secret, true},
		"last char differs":    {hash, secret[:len(secret)-1] + "x", false},
		"shorter secret":       {hash, secret[:10], false},
		"empty secret":         {hash, "", false},
		"truncated stored":     {hash[:16], secret, false},
		"empty stored":         {nil, secret, false},
		"hash used as secret":  {hash, string(hash), false},
		"stored is raw secret": {[]byte(secret), secret, false},
	} {
		if got := secretMatches(tc.stored, tc.secret); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}

// secretMatches must stay a constant-time compare of fixed-size digests:
// comparing the digest (not the secret) hides the secret's length, and
// subtle.ConstantTimeCompare hides where the first difference is.
func TestSecretMatchesComparesDigests(t *testing.T) {
	_, a, _ := newSecret(tokenSecretBytes)
	h := sha256.Sum256([]byte("x"))
	if secretMatches(a, "x") || !secretMatches(h[:], "x") {
		t.Fatal("digest compare")
	}
}

func TestParseSecret(t *testing.T) {
	for v, want := range map[string]struct {
		id     int64
		secret string
		ok     bool
	}{
		"stage_mt_7_abc":     {7, "abc", true},
		"stage_mt_7_a_b":     {7, "a_b", true},
		"stage_mt_7_":        {0, "", false},
		"stage_mt__abc":      {0, "", false},
		"stage_mt_0_abc":     {0, "", false},
		"stage_mt_-1_abc":    {0, "", false},
		"stage_mt_x_abc":     {0, "", false},
		"stage_mt_7":         {0, "", false},
		"stage_pc_7_abc":     {0, "", false},
		"Bearer stage_mt_7_": {0, "", false},
	} {
		id, secret, ok := parseSecret(MachineTokenPrefix, v)
		if id != want.id || secret != want.secret || ok != want.ok {
			t.Errorf("%q: %d %q %v", v, id, secret, ok)
		}
	}
}
