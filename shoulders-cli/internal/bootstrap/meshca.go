package bootstrap

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
)

// ClusterMeshCAPropagation shares one Cilium trust domain across clusters.
//
// Every cluster in a ClusterMesh must trust the same root CA. Cilium's `helm`
// certificate method generates a CA on first install, so the first cluster to
// be installed becomes the trust anchor and every other cluster must be seeded
// with its `cilium-ca` secret BEFORE its own Cilium install. Seeding after the
// fact means re-signing and restarting every agent.
//
// Hence the ordering constraint in the topology install: the hub goes first.
const (
	ciliumCASecretName = "cilium-ca"
	ciliumCANamespace  = "kube-system"
)

// CiliumCA retrieves a cluster's `cilium-ca` secret. The second return value
// reports whether it exists.
func CiliumCA(ctx context.Context, kubeconfigPath string) (*corev1.Secret, bool, error) {
	clientset, err := kube.NewClientset(kubeconfigPath)
	if err != nil {
		return nil, false, fmt.Errorf("create clientset: %w", err)
	}
	secret, err := clientset.CoreV1().Secrets(ciliumCANamespace).Get(ctx, ciliumCASecretName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read %s/%s: %w", ciliumCANamespace, ciliumCASecretName, err)
	}
	return secret, true, nil
}

// SeedCiliumCA copies a `cilium-ca` secret into another cluster, stripping the
// server-assigned metadata so it can be applied verbatim.
//
// Existing secrets are left alone unless overwrite is set: replacing a CA
// underneath a running cluster invalidates every certificate already issued
// from it.
func SeedCiliumCA(ctx context.Context, kubeconfigPath string, ca *corev1.Secret, overwrite bool) (bool, error) {
	if ca == nil {
		return false, fmt.Errorf("no cilium CA to seed")
	}
	if ca.Namespace == "" {
		ca.Namespace = ciliumCANamespace
	}

	clientset, err := kube.NewClientset(kubeconfigPath)
	if err != nil {
		return false, fmt.Errorf("create clientset: %w", err)
	}
	secrets := clientset.CoreV1().Secrets(ciliumCANamespace)

	existing, err := secrets.Get(ctx, ca.Name, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("read existing %s: %w", ca.Name, err)
		}
		if _, createErr := secrets.Create(ctx, sanitizedSecret(ca), metav1.CreateOptions{}); createErr != nil {
			return false, fmt.Errorf("seed %s: %w", ca.Name, createErr)
		}
		return true, nil
	}

	if !overwrite {
		return false, nil
	}
	candidate := sanitizedSecret(ca)
	candidate.ResourceVersion = existing.ResourceVersion
	if _, err := secrets.Update(ctx, candidate, metav1.UpdateOptions{}); err != nil {
		return false, fmt.Errorf("update %s: %w", ca.Name, err)
	}
	return true, nil
}

// sanitizedSecret drops the fields the API server assigns so a secret read from
// one cluster can be created in another.
func sanitizedSecret(secret *corev1.Secret) *corev1.Secret {
	out := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        secret.Name,
			Namespace:   secret.Namespace,
			Labels:      secret.Labels,
			Annotations: secret.Annotations,
		},
		Type: secret.Type,
		Data: secret.Data,
	}
	if out.Namespace == "" {
		out.Namespace = ciliumCANamespace
	}
	return out
}
