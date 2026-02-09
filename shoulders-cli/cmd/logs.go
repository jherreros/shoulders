package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs <app-name>",
	Short: "Fetch application logs (Loki if available)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := args[0]
		namespace, err := currentNamespace()
		if err != nil {
			return err
		}

		clientset, err := kube.NewClientset(kubeconfig)
		if err != nil {
			return err
		}

		if _, err := clientset.CoreV1().Services("observability").Get(context.Background(), "loki", metav1.GetOptions{}); err == nil {
			if err := queryLoki(context.Background(), appName); err == nil {
				return nil
			}
		}
		return streamPodLogs(context.Background(), clientset, namespace, appName)
	},
}

func init() {
	registerNamespaceFlag(logsCmd)
}

func queryLoki(ctx context.Context, appName string) error {
	stopCh, _, err := kube.PortForwardService(ctx, kubeconfig, "observability", "loki", 3100, 3100)
	if err != nil {
		return err
	}
	defer close(stopCh)

	client := &http.Client{Timeout: 10 * time.Second}
	query := fmt.Sprintf("http://localhost:3100/loki/api/v1/query?query={app=\"%s\"}", appName)
	resp, err := client.Get(query)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("loki query failed: %s", resp.Status)
	}
	_, err = io.Copy(os.Stdout, resp.Body)
	return err
}

func streamPodLogs(ctx context.Context, clientset *kubernetes.Clientset, namespace, appName string) error {
	selector := fmt.Sprintf("app=%s", appName)
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return err
	}
	if len(pods.Items) == 0 {
		return fmt.Errorf("no pods found for selector %s", selector)
	}
	for _, pod := range pods.Items {
		if err := streamSinglePodLog(ctx, clientset, namespace, pod.Name); err != nil {
			return err
		}
	}
	return nil
}

func streamSinglePodLog(ctx context.Context, clientset *kubernetes.Clientset, namespace, podName string) error {
	request := clientset.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{Follow: true})
	stream, err := request.Stream(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = stream.Close()
	}()
	_, err = io.Copy(os.Stdout, stream)
	return err
}
