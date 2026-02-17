package kube

import (
	"context"
	"fmt"

	authv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func CreateServiceAccountToken(ctx context.Context, kubeconfigPath, namespace, serviceAccount string) (string, error) {
	clientset, err := NewClientset(kubeconfigPath)
	if err != nil {
		return "", err
	}

	request := &authv1.TokenRequest{
		Spec: authv1.TokenRequestSpec{
			Audiences: []string{
				"https://kubernetes.default.svc",
				"https://kubernetes.default.svc.cluster.local",
				"kubernetes.default.svc",
			},
			ExpirationSeconds: int64Ptr(3600),
		},
	}

	response, err := clientset.CoreV1().ServiceAccounts(namespace).CreateToken(ctx, serviceAccount, request, createOptions())
	if err != nil {
		return "", err
	}
	if response == nil || response.Status.Token == "" {
		return "", fmt.Errorf("empty token response for service account %s/%s", namespace, serviceAccount)
	}
	return response.Status.Token, nil
}

func int64Ptr(value int64) *int64 {
	return &value
}

func createOptions() metav1.CreateOptions {
	return metav1.CreateOptions{}
}
