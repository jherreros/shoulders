package bootstrap

import (
	"fmt"
	"strings"
)

// SopsAgeSecretName is the Secret flux reads the age identity from. It is
// referenced by every Kustomization that carries a decryption block.
const SopsAgeSecretName = "sops-age"

// ParseSopsAgeIdentity extracts the identity line from an age key file.
//
// `age-keygen` writes two comment lines and the key; the comments are useful
// on disk and noise in a Secret. Returning an error when no identity is found
// matters: an empty Secret leaves flux reporting a decryption failure on every
// reconcile, far from the file that caused it.
func ParseSopsAgeIdentity(identity []byte) (string, error) {
	for _, line := range strings.Split(string(identity), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "AGE-SECRET-KEY-") {
			return line, nil
		}
	}
	return "", fmt.Errorf("no AGE-SECRET-KEY-1... line found: is this an age identity file?")
}

// sopsAgeSecretManifest renders the Secret flux decrypts with.
func sopsAgeSecretManifest(identity []byte) ([]byte, error) {
	key, err := ParseSopsAgeIdentity(identity)
	if err != nil {
		return nil, err
	}
	var builder strings.Builder
	builder.WriteString("apiVersion: v1\n")
	builder.WriteString("kind: Secret\n")
	builder.WriteString("metadata:\n")
	builder.WriteString("  name: " + SopsAgeSecretName + "\n")
	builder.WriteString("  namespace: flux-system\n")
	builder.WriteString("type: Opaque\n")
	builder.WriteString("stringData:\n")
	builder.WriteString("  age.agekey: |\n")
	builder.WriteString("    " + key + "\n")
	return []byte(builder.String()), nil
}
