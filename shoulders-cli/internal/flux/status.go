package flux

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

var kustomizationGVR = schema.GroupVersionResource{
	Group:    "kustomize.toolkit.fluxcd.io",
	Version:  "v1",
	Resource: "kustomizations",
}

var gitRepositoryGVR = schema.GroupVersionResource{
	Group:    "source.toolkit.fluxcd.io",
	Version:  "v1",
	Resource: "gitrepositories",
}

var ociRepositoryGVR = schema.GroupVersionResource{
	Group:    "source.toolkit.fluxcd.io",
	Version:  "v1",
	Resource: "ocirepositories",
}

type KustomizationReadiness struct {
	Name    string
	Reason  string
	Message string
}

// SourceReadiness mirrors KustomizationReadiness for the Flux source objects
// (GitRepository / OCIRepository named flux-system in flux-system).
type SourceReadiness struct {
	Kind    string
	Name    string
	Reason  string
	Message string
}

func ListKustomizations(ctx context.Context, client dynamic.Interface, namespace string) ([]unstructured.Unstructured, error) {
	resource := client.Resource(kustomizationGVR)
	var listResource dynamic.ResourceInterface = resource
	if namespace != "" {
		listResource = resource.Namespace(namespace)
	}
	list, err := listResource.List(ctx, listOptions())
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func AllKustomizationsReady(ctx context.Context, client dynamic.Interface, namespace string) (bool, []string, error) {
	pending, err := PendingKustomizations(ctx, client, namespace)
	if err != nil {
		return false, nil, err
	}
	names := make([]string, 0, len(pending))
	for _, item := range pending {
		names = append(names, item.Name)
	}
	return len(pending) == 0, names, nil
}

func PendingKustomizations(ctx context.Context, client dynamic.Interface, namespace string) ([]KustomizationReadiness, error) {
	items, err := ListKustomizations(ctx, client, namespace)
	if err != nil {
		return nil, err
	}
	pending := make([]KustomizationReadiness, 0)
	for _, item := range items {
		ready, _ := kube.HasCondition(item, "Ready", "True")
		if ready {
			continue
		}
		reason, message := readyConditionDetails(item)
		pending = append(pending, KustomizationReadiness{
			Name:    item.GetName(),
			Reason:  reason,
			Message: message,
		})
	}
	sort.Slice(pending, func(i, j int) bool {
		return pending[i].Name < pending[j].Name
	})
	return pending, nil
}

func FormatPending(pending []KustomizationReadiness) string {
	parts := make([]string, 0, len(pending))
	for _, item := range pending {
		parts = append(parts, item.summary())
	}
	return strings.Join(parts, ", ")
}

func FirstMissingPathFailure(pending []KustomizationReadiness) (KustomizationReadiness, bool) {
	for _, item := range pending {
		message := strings.ToLower(item.Message)
		if strings.Contains(message, "kustomization path not found") || strings.Contains(message, "no such file or directory") {
			return item, true
		}
	}
	return KustomizationReadiness{}, false
}

// PendingSources returns the flux-system GitRepository and OCIRepository
// objects that are not Ready. When neither source CRD is installed (older
// Flux) or neither object exists yet, it returns an empty slice and no error;
// callers should treat that as "unknown, keep waiting on Kustomizations".
func PendingSources(ctx context.Context, client dynamic.Interface, namespace string) ([]SourceReadiness, error) {
	pending := []SourceReadiness{}
	for _, gvr := range []schema.GroupVersionResource{gitRepositoryGVR, ociRepositoryGVR} {
		items, err := listSourceObjects(ctx, client, gvr, namespace)
		if err != nil {
			if isNotFoundError(err) {
				continue
			}
			return nil, err
		}
		for _, item := range items {
			ready, _ := kube.HasCondition(item, "Ready", "True")
			if ready {
				continue
			}
			reason, message := readyConditionDetails(item)
			pending = append(pending, SourceReadiness{
				Kind:    item.GetKind(),
				Name:    item.GetName(),
				Reason:  reason,
				Message: message,
			})
		}
	}
	sort.Slice(pending, func(i, j int) bool {
		if pending[i].Kind != pending[j].Kind {
			return pending[i].Kind < pending[j].Kind
		}
		return pending[i].Name < pending[j].Name
	})
	return pending, nil
}

// FirstSourcePullFailure reports the first pending source whose message looks
// like an artifact fetch/auth problem (wrong OCI url/tag, bad credentials,
// unreachable git remote). Callers use it to fail fast with a source hint
// instead of waiting out the Kustomization timeout.
func FirstSourcePullFailure(pending []SourceReadiness) (SourceReadiness, bool) {
	for _, item := range pending {
		message := strings.ToLower(item.Message)
		for _, marker := range []string{
			"artifact pull failed",
			"failed to pull artifact",
			"repository not found",
			"unauthorized",
			"authentication required",
			"no such host",
			"connection refused",
			"tls",
			"certificate",
			"fetch failed",
			"failed to checkout",
			"failed to clone",
		} {
			if strings.Contains(message, marker) {
				return item, true
			}
		}
	}
	return SourceReadiness{}, false
}

func listSourceObjects(ctx context.Context, client dynamic.Interface, gvr schema.GroupVersionResource, namespace string) ([]unstructured.Unstructured, error) {
	resource := client.Resource(gvr)
	var listResource dynamic.ResourceInterface = resource
	if namespace != "" {
		listResource = resource.Namespace(namespace)
	}
	list, err := listResource.List(ctx, listOptions())
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "not found") || strings.Contains(message, "no matches") || strings.Contains(message, "could not find")
}

func RequestKustomizationReconcile(ctx context.Context, client dynamic.Interface, namespace, name string, requestedAt time.Time) error {
	patch := reconcileRequestedAtPatch(requestedAt)
	payload, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	_, err = client.Resource(kustomizationGVR).Namespace(namespace).Patch(ctx, name, types.MergePatchType, payload, metav1.PatchOptions{})
	return err
}

// RequestSourceReconcile triggers a Flux source (GitRepository or
// OCIRepository) reconciliation. sourceKind is "GitRepository" or
// "OCIRepository".
func RequestSourceReconcile(ctx context.Context, client dynamic.Interface, namespace, sourceKind, name string, requestedAt time.Time) error {
	var gvr schema.GroupVersionResource
	switch sourceKind {
	case "OCIRepository":
		gvr = ociRepositoryGVR
	default:
		gvr = gitRepositoryGVR
	}
	patch := reconcileRequestedAtPatch(requestedAt)
	payload, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	_, err = client.Resource(gvr).Namespace(namespace).Patch(ctx, name, types.MergePatchType, payload, metav1.PatchOptions{})
	return err
}

func reconcileRequestedAtPatch(requestedAt time.Time) map[string]any {
	return map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]string{
				"reconcile.fluxcd.io/requestedAt": requestedAt.Format(time.RFC3339Nano),
			},
		},
	}
}

func listOptions() metav1.ListOptions {
	return metav1.ListOptions{}
}

func readyConditionDetails(item unstructured.Unstructured) (string, string) {
	conditions, ok, _ := unstructured.NestedSlice(item.Object, "status", "conditions")
	if !ok {
		return "", ""
	}
	for _, entry := range conditions {
		condition, ok := entry.(map[string]interface{})
		if !ok || condition["type"] != "Ready" {
			continue
		}
		return conditionString(condition, "reason"), conditionString(condition, "message")
	}
	return "", ""
}

func conditionString(condition map[string]interface{}, key string) string {
	value, ok := condition[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func (item KustomizationReadiness) summary() string {
	detail := item.Reason
	if item.Message != "" {
		if detail != "" {
			detail += ": "
		}
		detail += item.Message
	}
	if detail == "" {
		return item.Name
	}
	return fmt.Sprintf("%s (%s)", item.Name, truncate(detail, 120))
}

func truncate(value string, maxLength int) string {
	if len(value) <= maxLength {
		return value
	}
	if maxLength <= 3 {
		return value[:maxLength]
	}
	return value[:maxLength-3] + "..."
}

func KustomizationStatusSummary(ctx context.Context, client dynamic.Interface, namespace string) (string, error) {
	pending, err := PendingKustomizations(ctx, client, namespace)
	if err != nil {
		return "", err
	}
	if len(pending) == 0 {
		return "All Kustomizations Ready", nil
	}
	return fmt.Sprintf("Not Ready: %s", FormatPending(pending)), nil
}
