package kube

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

func NewDynamicClient(kubeconfig string) (dynamic.Interface, error) {
	config, err := NewRestConfig(kubeconfig)
	if err != nil {
		return nil, err
	}
	return dynamic.NewForConfig(config)
}

func NewClientset(kubeconfig string) (*kubernetes.Clientset, error) {
	config, err := NewRestConfig(kubeconfig)
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(config)
}

// HelmReleaseGVR returns the GroupVersionResource for Flux HelmRelease objects.
func HelmReleaseGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    "helm.toolkit.fluxcd.io",
		Version:  "v2",
		Resource: "helmreleases",
	}
}

// WaitForAPIServer polls the Kubernetes API server until it responds or the
// context times out. This is useful after starting Docker containers to
// ensure the control plane is ready before issuing further commands.
func WaitForAPIServer(ctx context.Context, kubeconfig string) error {
	clientset, err := NewClientset(kubeconfig)
	if err != nil {
		return fmt.Errorf("create clientset: %w", err)
	}
	return wait.PollUntilContextTimeout(ctx, 2*time.Second, 3*time.Minute, true, func(ctx context.Context) (bool, error) {
		_, err := clientset.Discovery().ServerVersion()
		return err == nil, nil
	})
}
