package bootstrap

import (
	"context"
	"fmt"
	"time"

	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	authConfigPath          = "/etc/kubernetes/authn/authentication-config.yaml"
	defaultDexServiceIP     = "10.96.0.24"
	dexNamespace            = "dex"
	dexServiceName          = "dex"
	dexInternalHosts        = "dex.dex.svc.cluster.local dex.dex.svc"
	dexPublicHost           = "dex.127.0.0.1.sslip.io"
	apiserverRestartTimeout = 2 * time.Minute
)

func WaitForDeploymentReady(kubeconfig, namespace, name string, timeout time.Duration) error {
	clientset, err := kube.NewClientset(kubeconfig)
	if err != nil {
		return err
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		deployment, err := clientset.AppsV1().Deployments(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err == nil && deploymentReady(deployment) {
			return nil
		}
		time.Sleep(5 * time.Second)
	}

	return fmt.Errorf("deployment %s/%s did not become ready within %s", namespace, name, timeout)
}

func WaitForStatefulSetReady(kubeconfig, namespace, name string, timeout time.Duration) error {
	clientset, err := kube.NewClientset(kubeconfig)
	if err != nil {
		return err
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		statefulSet, err := clientset.AppsV1().StatefulSets(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err == nil && statefulSetReady(statefulSet) {
			return nil
		}
		time.Sleep(5 * time.Second)
	}

	return fmt.Errorf("statefulset %s/%s did not become ready within %s", namespace, name, timeout)
}

func WaitForJobComplete(kubeconfig, namespace, name string, timeout time.Duration) error {
	clientset, err := kube.NewClientset(kubeconfig)
	if err != nil {
		return err
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		job, err := clientset.BatchV1().Jobs(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err == nil {
			if jobComplete(job) {
				return nil
			}
			if jobFailed(job) {
				return fmt.Errorf("job %s/%s failed", namespace, name)
			}
		}
		time.Sleep(5 * time.Second)
	}

	return fmt.Errorf("job %s/%s did not complete within %s", namespace, name, timeout)
}

func deploymentReady(deployment *appsv1.Deployment) bool {
	if deployment.Generation > deployment.Status.ObservedGeneration {
		return false
	}

	for _, condition := range deployment.Status.Conditions {
		if condition.Type == appsv1.DeploymentAvailable && condition.Status == "True" {
			return true
		}
	}

	return false
}

func statefulSetReady(statefulSet *appsv1.StatefulSet) bool {
	if statefulSet.Generation > statefulSet.Status.ObservedGeneration {
		return false
	}
	replicas := int32(1)
	if statefulSet.Spec.Replicas != nil {
		replicas = *statefulSet.Spec.Replicas
	}
	return statefulSet.Status.ReadyReplicas == replicas && statefulSet.Status.UpdatedReplicas == replicas
}

func jobComplete(job *batchv1.Job) bool {
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobComplete && condition.Status == "True" {
			return true
		}
	}
	return false
}

func jobFailed(job *batchv1.Job) bool {
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobFailed && condition.Status == "True" {
			return true
		}
	}
	return false
}
