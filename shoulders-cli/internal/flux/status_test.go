package flux

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestReadyConditionDetails(t *testing.T) {
	item := unstructured.Unstructured{Object: map[string]interface{}{
		"status": map[string]interface{}{
			"conditions": []interface{}{
				map[string]interface{}{
					"type":    "Ready",
					"status":  "False",
					"reason":  "BuildFailed",
					"message": "kustomization path not found: stat /tmp/source/2-addons/profiles/small/namespaces: no such file or directory",
				},
			},
		},
	}}

	reason, message := readyConditionDetails(item)
	if reason != "BuildFailed" {
		t.Fatalf("expected BuildFailed reason, got %q", reason)
	}
	if !strings.Contains(message, "kustomization path not found") {
		t.Fatalf("expected path-not-found message, got %q", message)
	}
}

func TestFirstMissingPathFailure(t *testing.T) {
	pending := []KustomizationReadiness{
		{Name: "headlamp", Reason: "DependencyNotReady", Message: "dependency 'namespaces' is not ready"},
		{Name: "namespaces", Reason: "BuildFailed", Message: "kustomization path not found: stat /tmp/source/2-addons/profiles/small/namespaces: no such file or directory"},
	}

	failure, ok := FirstMissingPathFailure(pending)
	if !ok {
		t.Fatalf("expected missing path failure")
	}
	if failure.Name != "namespaces" {
		t.Fatalf("expected namespaces failure, got %q", failure.Name)
	}
}

func TestFormatPendingIncludesDetails(t *testing.T) {
	pending := []KustomizationReadiness{
		{Name: "namespaces", Reason: "BuildFailed", Message: "short message"},
	}

	summary := FormatPending(pending)
	if summary != "namespaces (BuildFailed: short message)" {
		t.Fatalf("unexpected pending summary: %q", summary)
	}
}
