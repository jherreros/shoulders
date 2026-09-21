package bootstrap

import (
	"strings"
	"testing"
)

func TestParseNameservers(t *testing.T) {
	content := "# comment\nnameserver 1.1.1.1\nnameserver 127.0.0.11\nnameserver ::1\nsearch lan\nnameserver 8.8.8.8\n"
	servers := parseNameservers(content)
	if len(servers) != 2 || servers[0] != "1.1.1.1" || servers[1] != "8.8.8.8" {
		t.Fatalf("unexpected servers: %v", servers)
	}
	if servers := parseNameservers("nameserver 127.0.0.1\n"); len(servers) != 0 {
		t.Fatalf("expected loopback to be filtered, got %v", servers)
	}
}

func TestPatchCorefileForward(t *testing.T) {
	corefile := ".:53 {\n    errors\n    forward . /etc/resolv.conf\n    cache 30\n}\n"
	patched, changed := patchCorefileForward(corefile, []string{"1.1.1.1", "8.8.8.8"})
	if !changed {
		t.Fatalf("expected a change")
	}
	if !strings.Contains(patched, "forward . 1.1.1.1 8.8.8.8") {
		t.Fatalf("unexpected patched corefile:\n%s", patched)
	}
	if strings.Contains(patched, "/etc/resolv.conf") {
		t.Fatalf("stale forward survived:\n%s", patched)
	}
	// Idempotent: already correct.
	if _, changed := patchCorefileForward(patched, []string{"1.1.1.1", "8.8.8.8"}); changed {
		t.Fatalf("expected no change on second pass")
	}
	// Empty servers fall back to public resolvers.
	patched, changed = patchCorefileForward(corefile, nil)
	if !changed || !strings.Contains(patched, "forward . 1.1.1.1 8.8.8.8") {
		t.Fatalf("expected fallback servers, got:\n%s", patched)
	}
}
