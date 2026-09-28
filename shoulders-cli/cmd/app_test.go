package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestParseImageTag(t *testing.T) {
	image, tag := parseImageTag("nginx:1.26", "")
	if image != "nginx" || tag != "1.26" {
		t.Fatalf("expected nginx:1.26, got %s:%s", image, tag)
	}

	_, tag = parseImageTag("nginx", "")
	if tag != "latest" {
		t.Fatalf("expected latest, got %s", tag)
	}

	_, tag = parseImageTag("nginx", "custom")
	if tag != "custom" {
		t.Fatalf("expected custom, got %s", tag)
	}

	image, tag = parseImageTag("localhost:5000/team/api:dev", "")
	if image != "localhost:5000/team/api" || tag != "dev" {
		t.Fatalf("expected localhost registry image to parse, got %s:%s", image, tag)
	}
}

func TestParseEnvVars(t *testing.T) {
	env, err := parseEnvVars([]string{"LOG_LEVEL=debug", "EMPTY="})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(env) != 2 || env[0]["name"] != "LOG_LEVEL" || env[0]["value"] != "debug" {
		t.Fatalf("unexpected env parse result: %#v", env)
	}
}

func TestBuildHTTPProbeTiming(t *testing.T) {
	oldDelay, oldPeriod := appProbeInitialDelay, appProbePeriod
	defer func() { appProbeInitialDelay, appProbePeriod = oldDelay, oldPeriod }()

	appProbeInitialDelay, appProbePeriod = 5, 10
	probe := buildHTTPProbe("/ready", 80)
	if probe["initialDelaySeconds"] != int64(5) || probe["periodSeconds"] != int64(10) {
		t.Fatalf("expected default timing 5/10, got %#v", probe)
	}

	appProbeInitialDelay, appProbePeriod = 90, 15
	probe = buildHTTPProbe("/health", 8080)
	if probe["initialDelaySeconds"] != int64(90) || probe["periodSeconds"] != int64(15) {
		t.Fatalf("expected custom timing 90/15, got %#v", probe)
	}
	if probe["httpGet"].(map[string]interface{})["port"] != int64(8080) {
		t.Fatalf("expected int64 port for unstructured safety, got %#v", probe["httpGet"])
	}

	if buildHTTPProbe("", 80) != nil {
		t.Fatal("expected nil probe for empty path")
	}
}

func TestApplyProbeTimingPatchesExistingProbes(t *testing.T) {
	oldDelay, oldPeriod := appProbeInitialDelay, appProbePeriod
	defer func() { appProbeInitialDelay, appProbePeriod = oldDelay, oldPeriod }()

	appProbeInitialDelay, appProbePeriod = 60, 20
	spec := map[string]interface{}{
		"readinessProbe": map[string]interface{}{"initialDelaySeconds": int64(5)},
		"livenessProbe":  nil,
	}
	if !applyProbeTiming(spec, true, true) {
		t.Fatal("expected timing patch to report changes")
	}
	got := spec["readinessProbe"].(map[string]interface{})
	if got["initialDelaySeconds"] != int64(60) || got["periodSeconds"] != int64(20) {
		t.Fatalf("expected patched timing 60/20, got %#v", got)
	}
	if _, ok := spec["livenessProbe"]; ok && spec["livenessProbe"] != nil {
		t.Fatalf("expected nil probe left alone, got %#v", spec["livenessProbe"])
	}

	if applyProbeTiming(map[string]interface{}{}, true, true) {
		t.Fatal("expected no changes when no probes present")
	}

	// Retuning only the delay must preserve a customized period.
	spec = map[string]interface{}{
		"readinessProbe": map[string]interface{}{"initialDelaySeconds": int64(30), "periodSeconds": int64(20)},
	}
	appProbeInitialDelay = 45
	if !applyProbeTiming(spec, true, false) {
		t.Fatal("expected timing patch to report changes")
	}
	got = spec["readinessProbe"].(map[string]interface{})
	if got["initialDelaySeconds"] != int64(45) || got["periodSeconds"] != int64(20) {
		t.Fatalf("expected delay 45 with preserved period 20, got %#v", got)
	}
}

func TestBuildVolumesAndMounts(t *testing.T) {
	volumes, mounts, err := buildVolumesAndMounts([]string{"jwt-secret:/keys:jwt"}, []string{"tmp:/tmp"}, []string{"otel-config:/etc/otel:otel"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(volumes) != 3 || len(mounts) != 3 {
		t.Fatalf("expected three volumes and mounts, got %#v %#v", volumes, mounts)
	}
	foundConfigMap := false
	for _, volume := range volumes {
		if source, ok := volume["configMap"].(map[string]interface{}); ok && source["name"] == "otel-config" {
			foundConfigMap = true
		}
	}
	if !foundConfigMap {
		t.Fatalf("expected configMap volume source for otel-config, got %#v", volumes)
	}

	if _, _, err := buildVolumesAndMounts(nil, nil, []string{"bad-entry"}, nil); err == nil {
		t.Fatal("expected error for invalid configmap mount")
	}
}

func TestBuildVolumesAndMountsSecretItems(t *testing.T) {
	items, err := parseSecretItems([]string{"jwt:publickey=jwtRS256.key.pub,privatekey=jwtRS256.key", "jwt:extra=extra.key"})
	if err != nil {
		t.Fatalf("parse secret items: %v", err)
	}
	volumes, _, err := buildVolumesAndMounts([]string{"jwt:/keys"}, nil, nil, items)
	if err != nil {
		t.Fatalf("build volumes: %v", err)
	}
	source, ok := volumes[0]["secret"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected secret source, got %#v", volumes[0])
	}
	mapped, ok := source["items"].([]interface{})
	if !ok || len(mapped) != 3 {
		t.Fatalf("expected three mapped items, got %#v", source["items"])
	}

	for _, bad := range []string{"nokeys", ":key=path", "jwt:keyonly", "jwt:=path"} {
		if _, err := parseSecretItems([]string{bad}); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}

	orphan, err := parseSecretItems([]string{"ghost:key=path"})
	if err != nil {
		t.Fatalf("parse orphan items: %v", err)
	}
	if _, _, err := buildVolumesAndMounts([]string{"jwt:/keys"}, nil, nil, orphan); err == nil {
		t.Fatal("expected error for items on an unmounted secret")
	}
}

func TestParseFieldRefEnv(t *testing.T) {
	env, err := parseFieldRefEnv([]string{"NAMESPACE=metadata.namespace", "POD_IP=status.podIP"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(env) != 2 {
		t.Fatalf("expected two entries, got %#v", env)
	}
	valueFrom, ok := env[0]["valueFrom"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected valueFrom map, got %#v", env[0])
	}
	fieldRef, ok := valueFrom["fieldRef"].(map[string]interface{})
	if !ok || fieldRef["fieldPath"] != "metadata.namespace" {
		t.Fatalf("expected metadata.namespace fieldRef, got %#v", valueFrom)
	}

	for _, bad := range []string{"NOEQUALS", "=metadata.namespace", "NAME="} {
		if _, err := parseFieldRefEnv([]string{bad}); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestBuildResourcesEphemeralStorage(t *testing.T) {
	oldReq, oldLim := appEphemeralRequest, appEphemeralLimit
	defer func() { appEphemeralRequest, appEphemeralLimit = oldReq, oldLim }()

	appEphemeralRequest, appEphemeralLimit = "1Gi", "2Gi"
	resources := buildResources()
	requests, ok := resources["requests"].(map[string]interface{})
	if !ok || requests["ephemeral-storage"] != "1Gi" {
		t.Fatalf("expected ephemeral-storage request, got %#v", resources)
	}
	limits, ok := resources["limits"].(map[string]interface{})
	if !ok || limits["ephemeral-storage"] != "2Gi" {
		t.Fatalf("expected ephemeral-storage limit, got %#v", resources)
	}
}

func TestBuildPodSecurityContext(t *testing.T) {
	oldFsGroup := appFsGroup
	defer func() { appFsGroup = oldFsGroup }()

	appFsGroup = -1
	if buildPodSecurityContext() != nil {
		t.Fatal("expected nil pod security context when fs-group unset")
	}
	appFsGroup = 2000
	got := buildPodSecurityContext()
	if got["fsGroup"] != int64(2000) {
		t.Fatalf("expected fsGroup 2000, got %#v", got)
	}
}

// TestApplyAppFlagOverridesUnstructuredSafe is a regression test for the
// `app update --env` panic ("cannot deep copy []map[string]interface {}").
// Flag overrides must land in the spec in unstructured-safe form.
func TestApplyAppFlagOverridesUnstructuredSafe(t *testing.T) {
	oldEnv, oldConfigMaps, oldSecretMounts, oldEmptyDirs, oldCommand, oldArgs := appEnv, appEnvFromConfigMaps, appSecretMounts, appEmptyDirMounts, appCommand, appArgs
	oldReplicas, oldPort, oldServicePort, oldDelay, oldPeriod := appReplicas, appPort, appServicePort, appProbeInitialDelay, appProbePeriod
	defer func() {
		appEnv, appEnvFromConfigMaps, appSecretMounts, appEmptyDirMounts, appCommand, appArgs = oldEnv, oldConfigMaps, oldSecretMounts, oldEmptyDirs, oldCommand, oldArgs
		appReplicas, appPort, appServicePort, appProbeInitialDelay, appProbePeriod = oldReplicas, oldPort, oldServicePort, oldDelay, oldPeriod
	}()

	cmd := &cobra.Command{Use: "test"}
	registerAppSpecFlags(cmd, false)
	if err := cmd.ParseFlags([]string{
		"--env", "FOO=bar",
		"--env-from-configmap", "my-config",
		"--secret-mount", "jwt-secret:/keys",
		"--empty-dir", "tmp:/tmp",
		"--command", "/bin/sh",
		"--arg", "-c",
		"--arg", "echo hi",
		"--replicas", "3",
		"--port", "8080",
		"--service-port", "80",
		"--readiness-path", "/ready",
		"--probe-initial-delay", "60",
	}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	spec := map[string]interface{}{"image": "nginx", "tag": "latest"}
	changed, err := applyAppFlagOverrides(cmd, "test-app", spec)
	if err != nil {
		t.Fatalf("applyAppFlagOverrides: %v", err)
	}
	if !changed {
		t.Fatal("expected changes to be reported")
	}

	// This panicked pre-fix when spec values were []map[string]interface{}.
	obj := &unstructured.Unstructured{Object: map[string]interface{}{"spec": spec}}
	copied := obj.DeepCopy()
	env, ok, err := unstructured.NestedSlice(copied.Object, "spec", "env")
	if err != nil || !ok || len(env) != 1 {
		t.Fatalf("expected one env entry after deep copy, got %#v (err=%v)", env, err)
	}
	if _, ok := env[0].(map[string]interface{}); !ok {
		t.Fatalf("expected env entry to be a map, got %T", env[0])
	}
	for _, path := range [][]string{{"spec", "envFrom"}, {"spec", "volumes"}, {"spec", "volumeMounts"}} {
		if _, ok, err := unstructured.NestedSlice(copied.Object, path...); err != nil || !ok {
			t.Fatalf("expected %v to survive deep copy (ok=%v err=%v)", path, ok, err)
		}
	}
	command, _, err := unstructured.NestedStringSlice(copied.Object, "spec", "command")
	if err != nil || len(command) != 1 || command[0] != "/bin/sh" {
		t.Fatalf("expected command [/bin/sh], got %#v (err=%v)", command, err)
	}
	args, _, err := unstructured.NestedStringSlice(copied.Object, "spec", "args")
	if err != nil || len(args) != 2 || args[0] != "-c" || args[1] != "echo hi" {
		t.Fatalf("expected args [-c echo hi], got %#v (err=%v)", args, err)
	}
	// Scalar overrides must also be unstructured-safe (int64, not int32):
	// pre-fix, --replicas/--port/--service-port/--readiness-path panicked
	// with "cannot deep copy int32" on SetNestedMap.
	if replicas, ok, err := unstructured.NestedInt64(copied.Object, "spec", "replicas"); err != nil || !ok || replicas != 3 {
		t.Fatalf("expected replicas 3, got %v (ok=%v err=%v)", replicas, ok, err)
	}
	if port, ok, err := unstructured.NestedInt64(copied.Object, "spec", "port"); err != nil || !ok || port != 8080 {
		t.Fatalf("expected port 8080, got %v (ok=%v err=%v)", port, ok, err)
	}
	if svcPort, ok, err := unstructured.NestedInt64(copied.Object, "spec", "service", "port"); err != nil || !ok || svcPort != 80 {
		t.Fatalf("expected service.port 80, got %v (ok=%v err=%v)", svcPort, ok, err)
	}
	if delay, ok, err := unstructured.NestedInt64(copied.Object, "spec", "readinessProbe", "initialDelaySeconds"); err != nil || !ok || delay != 60 {
		t.Fatalf("expected probe delay 60, got %v (ok=%v err=%v)", delay, ok, err)
	}
}
