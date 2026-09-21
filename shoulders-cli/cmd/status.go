package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jherreros/shoulders/shoulders-cli/internal/crossplane"
	"github.com/jherreros/shoulders/shoulders-cli/internal/flux"
	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	"github.com/jherreros/shoulders/shoulders-cli/internal/output"
	"github.com/jherreros/shoulders/shoulders-cli/internal/tui"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type statusSummary struct {
	K8sVersion   string   `json:"k8sVersion" yaml:"k8sVersion"`
	NodesReady   bool     `json:"nodesReady" yaml:"nodesReady"`
	NodeCount    int      `json:"nodeCount" yaml:"nodeCount"`
	TotalPods    int      `json:"totalPods" yaml:"totalPods"`
	HealthyPods  int      `json:"healthyPods" yaml:"healthyPods"`
	FluxReady    bool     `json:"fluxReady" yaml:"fluxReady"`
	FluxBroken   []string `json:"fluxBroken" yaml:"fluxBroken"`
	XPlaneReady  bool     `json:"crossplaneReady" yaml:"crossplaneReady"`
	XPlaneBroken []string `json:"crossplaneBroken" yaml:"crossplaneBroken"`
	GatewayReady bool     `json:"gatewayReady" yaml:"gatewayReady"`
	GatewayAddr  string   `json:"gatewayAddress" yaml:"gatewayAddress"`
}

var statusWait bool

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show cluster and platform status",
	RunE: func(cmd *cobra.Command, args []string) error {
		format, err := outputOption()
		if err != nil {
			return err
		}

		if statusWait {
			return statusWaitLoop(cmd.Context(), format)
		}

		summary, err := gatherStatus(cmd.Context())
		if err != nil {
			return err
		}
		return renderStatus(summary, format)
	},
}

func gatherStatus(ctx context.Context) (statusSummary, error) {
	clientset, err := kube.NewClientset(kubeconfig)
	if err != nil {
		return statusSummary{}, err
	}
	dynamicClient, err := kube.NewDynamicClient(kubeconfig)
	if err != nil {
		return statusSummary{}, err
	}

	// 1. Cluster Info
	versionInfo, err := clientset.Discovery().ServerVersion()
	if err != nil {
		return statusSummary{}, err
	}
	k8sVersion := versionInfo.GitVersion

	// 2. Nodes
	nodes, err := clientset.CoreV1().Nodes().List(ctx, v1.ListOptions{})
	if err != nil {
		return statusSummary{}, err
	}
	nodesReady := true
	for _, node := range nodes.Items {
		if !isNodeReady(node) {
			nodesReady = false
		}
	}

	// 3. Flux
	fluxReady, fluxPending, err := flux.AllKustomizationsReady(ctx, dynamicClient, "flux-system")
	if err != nil {
		fluxReady = false
		fluxPending = []string{err.Error()}
	}
	// Surface OCI/Git source pull failures directly: with source: oci a wiped
	// local registry leaves Kustomizations pending with unhelpful messages.
	if sources, srcErr := flux.PendingSources(ctx, dynamicClient, "flux-system"); srcErr == nil {
		if failed, ok := flux.FirstSourcePullFailure(sources); ok {
			fluxReady = false
			fluxPending = append(fluxPending, fmt.Sprintf("%s/%s: %s", failed.Kind, failed.Name, failed.Message))
		}
	}

	// 4. Crossplane
	xpReady, xpUnhealthy, err := crossplane.AllProvidersHealthy(ctx, dynamicClient)
	if err != nil {
		xpReady = false
		xpUnhealthy = []string{err.Error()}
	}

	// 5. Pods
	podList, err := clientset.CoreV1().Pods("").List(ctx, v1.ListOptions{})
	if err != nil {
		return statusSummary{}, err
	}
	totalPods := 0
	healthyPods := 0
	for _, pod := range podList.Items {
		if shouldIgnorePodForHealth(pod) {
			continue
		}
		totalPods++
		if isPodHealthy(pod) {
			healthyPods++
		}
	}

	// 6. Gateway
	gwReady := true
	gwAddr := "Externally managed"
	if gatewayChecksRequired() {
		gwReady = false
		gwAddr = "Pending"
		gwProgrammed := false
		gvrGW := schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"}
		gwList, err := dynamicClient.Resource(gvrGW).Namespace("kube-system").List(ctx, v1.ListOptions{})
		if err != nil && !apierrors.IsNotFound(err) && !apimeta.IsNoMatchError(err) {
			return statusSummary{}, err
		}
		if err == nil && len(gwList.Items) > 0 {
			gw := gwList.Items[0]
			if status, ok := gw.Object["status"].(map[string]interface{}); ok {
				if conditions, ok := status["conditions"].([]interface{}); ok {
					for _, c := range conditions {
						cond, _ := c.(map[string]interface{})
						if fmt.Sprintf("%v", cond["type"]) == "Programmed" && fmt.Sprintf("%v", cond["status"]) == "True" {
							gwProgrammed = true
							gwReady = true
						}
					}
				}
				if addrs, ok := status["addresses"].([]interface{}); ok && len(addrs) > 0 {
					if addrMap, ok := addrs[0].(map[string]interface{}); ok {
						gwAddr = fmt.Sprintf("%v", addrMap["value"])
					}
				}
			}
		}
		if !gwProgrammed {
			if svc, err := clientset.CoreV1().Services("kube-system").Get(ctx, "cilium-gateway-cilium-gateway", v1.GetOptions{}); err == nil {
				if svc.Spec.Type == corev1.ServiceTypeClusterIP {
					gwReady = true
					gwAddr = "localhost"
				}
			}
		}
	}

	return statusSummary{
		K8sVersion:   k8sVersion,
		NodesReady:   nodesReady,
		NodeCount:    len(nodes.Items),
		TotalPods:    totalPods,
		HealthyPods:  healthyPods,
		FluxReady:    fluxReady,
		FluxBroken:   fluxPending,
		XPlaneReady:  xpReady,
		XPlaneBroken: xpUnhealthy,
		GatewayReady: gwReady,
		GatewayAddr:  gwAddr,
	}, nil
}

func renderStatus(s statusSummary, format output.Format) error {
	if format == output.Table {
		printStatusTable(s)
		return nil
	}
	payload, err := output.Render(s, format)
	if err != nil {
		return err
	}
	fmt.Println(string(payload))
	return nil
}

func statusWaitLoop(ctx context.Context, format output.Format) error {
	area, _ := pterm.DefaultArea.WithRemoveWhenDone(false).Start()
	defer func() { _ = area.Stop() }()

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		summary, err := gatherStatus(ctx)
		if err != nil {
			return err
		}
		podsHealthy := summary.TotalPods > 0 && summary.HealthyPods == summary.TotalPods

		area.Update(renderStatusTUI(summary))

		if summary.NodesReady && podsHealthy && summary.FluxReady && summary.XPlaneReady && summary.GatewayReady {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func isNodeReady(node corev1.Node) bool {
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func isPodHealthy(pod corev1.Pod) bool {
	switch pod.Status.Phase {
	case corev1.PodSucceeded:
		return true
	case corev1.PodRunning:
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				return true
			}
		}
	}
	return false
}

func shouldIgnorePodForHealth(pod corev1.Pod) bool {
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "Job" {
			return true
		}
	}
	return false
}

func renderStatusTUI(s statusSummary) string {
	var b strings.Builder

	b.WriteString(tui.Header("Shoulders Platform Status") + "\n\n")
	b.WriteString(tui.StatusLine("Cluster", s.NodesReady, fmt.Sprintf("%s • %d node(s)", s.K8sVersion, s.NodeCount)) + "\n")
	podsHealthy := s.TotalPods > 0 && s.HealthyPods == s.TotalPods
	b.WriteString(tui.StatusLine("Pods", podsHealthy, fmt.Sprintf("%d/%d healthy", s.HealthyPods, s.TotalPods)) + "\n")
	b.WriteString(tui.StatusLine("Flux CD", s.FluxReady, formatDetail(s.FluxBroken)) + "\n")
	b.WriteString(tui.StatusLine("Crossplane", s.XPlaneReady, formatDetail(s.XPlaneBroken)) + "\n")
	b.WriteString(tui.StatusLine("Gateway", s.GatewayReady, s.GatewayAddr) + "\n")

	if s.NodesReady && podsHealthy && s.FluxReady && s.XPlaneReady && s.GatewayReady {
		b.WriteString("\n  " + pterm.NewStyle(pterm.FgGreen, pterm.Bold).Sprint("All systems healthy") + "\n")
	} else {
		b.WriteString("\n  " + pterm.NewStyle(pterm.FgYellow).Sprint("Waiting for components to become healthy...") + "\n")
	}

	return b.String()
}

func printStatusTable(s statusSummary) {
	fmt.Println(renderStatusTUI(s))
}

func formatStatus(ready bool, issues []string) string {
	if ready {
		return "Healthy"
	}
	if len(issues) > 0 {
		return fmt.Sprintf("Unhealthy (%s)", strings.Join(issues, ", "))
	}
	return "Unhealthy"
}

func formatDetail(issues []string) string {
	if len(issues) > 0 {
		return strings.Join(issues, ", ")
	}
	return ""
}

func boolToText(b bool) string {
	if b {
		return "Ready"
	}
	return "Not Ready"
}

func init() {
	statusCmd.Flags().BoolVar(&statusWait, "wait", false, "Poll until all components are healthy")
}
