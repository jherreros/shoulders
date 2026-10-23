package bootstrap

import (
	"strings"
	"testing"
)

// TestParseSopsAgeIdentityKeepsOnlyTheKey: age-keygen writes comments, and a
// Secret should not carry them.
func TestParseSopsAgeIdentityKeepsOnlyTheKey(t *testing.T) {
	key := "AGE-SECRET-KEY-1QQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQ"
	file := "# created by age-keygen\n# public key: age1abc\n" + key + "\n"

	got, err := ParseSopsAgeIdentity([]byte(file))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if got != key {
		t.Fatalf("identity = %q, want the key line", got)
	}

	if _, err := ParseSopsAgeIdentity([]byte("# only comments\n")); err == nil {
		t.Fatal("a file with no identity must be an error, not an empty Secret")
	}
}

// TestSopsAgeSecretCarriesTheIdentity is the contract flux depends on: the
// Secret is named sops-age and the key is stored under age.agekey.
func TestSopsAgeSecretCarriesTheIdentity(t *testing.T) {
	key := "AGE-SECRET-KEY-1QQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQ"
	manifest, err := sopsAgeSecretManifest([]byte("# comment\n" + key + "\n"))
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	text := string(manifest)
	for _, want := range []string{
		"name: sops-age",
		"namespace: flux-system",
		"age.agekey: |",
		"    " + key,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the secret is missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "# comment") {
		t.Error("the secret must not carry the key file's comments")
	}

	if _, err := sopsAgeSecretManifest([]byte("not a key")); err == nil {
		t.Fatal("expected an error for an invalid identity")
	}
}
