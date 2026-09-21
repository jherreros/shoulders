package ocisource

import (
	"context"
	"fmt"
	"time"

	"github.com/jherreros/shoulders/shoulders-cli/internal/bootstrap"
	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// RegistryNamespace hosts the throwaway OCI registry for local iteration.
	// flux-system keeps the in-cluster pull URL short and needs no extra
	// namespace lifecycle.
	RegistryNamespace = "flux-system"
	RegistryName      = "shoulders-registry"
	RegistryPort      = 5000
	// RegistryImage is pulled when the local registry is first ensured. It
	// needs network access at ensure time (fine for the dev loop; the
	// airgap bundle in Phase 2 vendors it).
	RegistryImage = "registry:2.8.3"
	// RegistryRepoPath is the repository inside the registry holding the
	// Shoulders addon snapshots.
	RegistryRepoPath = "shoulders/addons"
	// RegistryDataPVC is the persistent volume claim backing registry
	// storage so pushed snapshots survive restarts and stop/start cycles.
	// Name, size, and class must stay stable: PVCs are immutable and the
	// ensure path re-applies this manifest.
	RegistryDataPVC = "shoulders-registry-data"
	// RegistryDataSize is deliberately small: addon snapshots are a few MB.
	RegistryDataSize = "5Gi"
)

// PullURL is the in-cluster address Flux source-controller pulls from.
func PullURL() string {
	return fmt.Sprintf("oci://%s.%s.svc.cluster.local:%d/%s", RegistryName, RegistryNamespace, RegistryPort, RegistryRepoPath)
}

// RegistryAddr is the in-cluster registry host:port (no scheme), for
// building OCI chart URLs at bundle install time.
func RegistryAddr() string {
	return fmt.Sprintf("%s.%s.svc.cluster.local:%d", RegistryName, RegistryNamespace, RegistryPort)
}

// RegistryManifest renders the Namespace + Deployment + Service for the
// local registry. The Namespace is included so the registry can be ensured
// before Flux itself is installed (Flux creates flux-system on install).
// The data PVC is managed separately (create-only) in EnsureLocalRegistry.
func RegistryManifest() []byte {
	return []byte(fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %[1]s
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %[2]s
  namespace: %[1]s
  labels:
    app.kubernetes.io/name: %[2]s
    app.kubernetes.io/part-of: shoulders
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: %[2]s
  template:
    metadata:
      labels:
        app.kubernetes.io/name: %[2]s
    spec:
      containers:
        - name: registry
          image: %[3]s
          imagePullPolicy: IfNotPresent
          ports:
            - name: http
              containerPort: %[4]d
          env:
            - name: REGISTRY_STORAGE_DELETE_ENABLED
              value: "false"
          volumeMounts:
            - name: data
              mountPath: /var/lib/registry
          readinessProbe:
            httpGet:
              path: /v2/
              port: http
            periodSeconds: 5
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: %[5]s
---
apiVersion: v1
kind: Service
metadata:
  name: %[2]s
  namespace: %[1]s
  labels:
    app.kubernetes.io/name: %[2]s
spec:
  selector:
    app.kubernetes.io/name: %[2]s
  ports:
    - name: http
      port: %[4]d
      targetPort: http
`, RegistryNamespace, RegistryName, RegistryImage, RegistryPort, RegistryDataPVC))
}

// RegistryNamespaceManifest renders the flux-system Namespace so the
// registry (and its data volume) can be ensured before Flux is installed.
func RegistryNamespaceManifest() []byte {
	return []byte(fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
`, RegistryNamespace))
}

// RegistryDataPVCManifest renders the persistent volume claim backing
// registry storage so pushed snapshots survive restarts and stop/start
// cycles.
func RegistryDataPVCManifest() []byte {
	return []byte(fmt.Sprintf(`apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %[2]s
  namespace: %[1]s
  labels:
    app.kubernetes.io/name: %[3]s
    app.kubernetes.io/part-of: shoulders
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: %[4]s
`, RegistryNamespace, RegistryDataPVC, RegistryName, RegistryDataSize))
}

// EnsureLocalRegistry applies the registry Deployment + Service and waits
// until the Deployment is ready to accept pushes. The data PVC is created
// once and never updated: bound claims reject spec changes, and registry
// data must survive re-applies.
func EnsureLocalRegistry(ctx context.Context, kubeconfigPath string) error {
	if err := ensureRegistryDataPVC(ctx, kubeconfigPath); err != nil {
		return err
	}
	if err := kube.ApplyManifest(ctx, kubeconfigPath, RegistryManifest(), RegistryNamespace); err != nil {
		return fmt.Errorf("apply local OCI registry: %w", err)
	}
	if err := bootstrap.WaitForDeploymentReady(kubeconfigPath, RegistryNamespace, RegistryName, 5*time.Minute); err != nil {
		return fmt.Errorf("wait for local OCI registry: %w", err)
	}
	return nil
}

func ensureRegistryDataPVC(ctx context.Context, kubeconfigPath string) error {
	clientset, err := kube.NewClientset(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("create clientset: %w", err)
	}
	if _, err := clientset.CoreV1().PersistentVolumeClaims(RegistryNamespace).Get(ctx, RegistryDataPVC, metav1.GetOptions{}); err == nil {
		return nil
	}
	// The namespace may not exist yet (pre-Flux install): create it along
	// with the claim. ApplyManifest only defaults namespaces, never creates.
	if err := kube.ApplyManifest(ctx, kubeconfigPath, RegistryNamespaceManifest(), ""); err != nil {
		return fmt.Errorf("apply local OCI registry namespace: %w", err)
	}
	if err := kube.ApplyManifest(ctx, kubeconfigPath, RegistryDataPVCManifest(), RegistryNamespace); err != nil {
		return fmt.Errorf("apply local OCI registry data volume: %w", err)
	}
	return nil
}
