package bootstrap

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestSanitizedSecretDropsServerFields guards the CA propagation path: a secret
// read from one cluster carries server-assigned fields that must be stripped
// before it can be created in another, or the API server rejects it.
func TestSanitizedSecretDropsServerFields(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "cilium-ca",
			Namespace:       "kube-system",
			ResourceVersion: "12345",
			UID:             "abcdef",
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"ca.crt": []byte("cert"), "ca.key": []byte("key")},
	}

	out := sanitizedSecret(secret)
	if out.ResourceVersion != "" {
		t.Fatalf("resourceVersion survived: %q", out.ResourceVersion)
	}
	if out.UID != "" {
		t.Fatalf("uid survived: %q", out.UID)
	}
	if out.Name != "cilium-ca" || out.Namespace != "kube-system" {
		t.Fatalf("identity lost: %+v", out.ObjectMeta)
	}
	if len(out.Data) != 2 || string(out.Data["ca.crt"]) != "cert" {
		t.Fatalf("CA payload lost: %+v", out.Data)
	}
}

func TestSanitizedSecretDefaultsNamespace(t *testing.T) {
	out := sanitizedSecret(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "cilium-ca"},
	})
	if out.Namespace != ciliumCANamespace {
		t.Fatalf("namespace = %q, want %q", out.Namespace, ciliumCANamespace)
	}
}
