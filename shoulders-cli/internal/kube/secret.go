package kube

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

const (
	grafanaAdminUserKey     = "admin-user"
	grafanaAdminPasswordKey = "admin-password"
)

type BasicAuthCredentials struct {
	Username string
	Password string
}

func GetSecret(ctx context.Context, kubeconfigPath, namespace, name string) (*corev1.Secret, error) {
	clientset, err := NewClientset(kubeconfigPath)
	if err != nil {
		return nil, err
	}
	return clientset.CoreV1().Secrets(namespace).Get(ctx, name, getOptions())
}

func GetGrafanaCredentials(ctx context.Context, kubeconfigPath, namespace, secretName string) (BasicAuthCredentials, error) {
	secret, err := GetSecret(ctx, kubeconfigPath, namespace, secretName)
	if err != nil {
		return BasicAuthCredentials{}, err
	}

	userBytes, userOk := secret.Data[grafanaAdminUserKey]
	passBytes, passOk := secret.Data[grafanaAdminPasswordKey]
	if !userOk || !passOk {
		return BasicAuthCredentials{}, fmt.Errorf("missing %q or %q in secret %s/%s", grafanaAdminUserKey, grafanaAdminPasswordKey, namespace, secretName)
	}

	return BasicAuthCredentials{
		Username: string(userBytes),
		Password: string(passBytes),
	}, nil
}
