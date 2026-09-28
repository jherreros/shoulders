package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jherreros/shoulders/shoulders-cli/internal/bootstrap"
	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	"github.com/jherreros/shoulders/shoulders-cli/internal/output"
	"github.com/jherreros/shoulders/shoulders-cli/pkg/api/v1alpha1"
	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"
)

var (
	appImage             string
	appTag               string
	appHost              string
	appPort              int32
	appServicePort       int32
	appReplicas          int32
	appDryRun            bool
	appInternal          bool
	appCommand           []string
	appArgs              []string
	appEnv               []string
	appEnvFromConfigMaps []string
	appEnvFromSecrets    []string
	appEnvFieldRef       []string
	appSecretMounts      []string
	appEmptyDirMounts    []string
	appConfigMapMounts   []string
	appSecretItems       []string
	appReadinessPath     string
	appLivenessPath      string
	appStartupPath       string
	appProbeInitialDelay int32
	appProbePeriod       int32
	appCPURequest        string
	appMemoryRequest     string
	appCPULimit          string
	appMemoryLimit       string
	appEphemeralRequest  string
	appEphemeralLimit    string
	appReadOnlyRootFS    bool
	appRunAsNonRoot      bool
	appRunAsUser         int64
	appFsGroup           int64
	appApplyFilename     string
	appImageCluster      string
)

var appCmd = &cobra.Command{
	Use:   "app",
	Short: "Manage WebApplications",
}

var appInitCmd = &cobra.Command{
	Use:   "init <name>",
	Short: "Create a WebApplication",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		namespace, err := currentNamespace()
		if err != nil {
			return err
		}

		app, err := buildWebApplication(name, namespace)
		if err != nil {
			return err
		}

		yamlBytes, err := yaml.Marshal(app)
		if err != nil {
			return err
		}
		if appDryRun {
			fmt.Println(string(yamlBytes))
			return nil
		}

		if err := applyWebApplication(cmd.Context(), namespace, yamlBytes); err != nil {
			return err
		}
		fmt.Printf("WebApplication %s applied in namespace %s\n", name, namespace)
		return nil
	},
}

var appUpdateCmd = &cobra.Command{
	Use:   "update <name>",
	Short: "Update a WebApplication",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		namespace, err := currentNamespace()
		if err != nil {
			return err
		}

		dynamicClient, err := kube.NewDynamicClient(kubeconfig)
		if err != nil {
			return err
		}
		gvr := schema.GroupVersionResource{Group: v1alpha1.Group, Version: v1alpha1.Version, Resource: "webapplications"}
		obj, err := dynamicClient.Resource(gvr).Namespace(namespace).Get(cmd.Context(), name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		spec, ok, err := unstructured.NestedMap(obj.Object, "spec")
		if err != nil {
			return err
		}
		if !ok {
			spec = map[string]interface{}{}
		}
		changed, err := applyAppFlagOverrides(cmd, name, spec)
		if err != nil {
			return err
		}
		if !changed {
			return fmt.Errorf("no updates requested; pass at least one app update flag")
		}
		if err := unstructured.SetNestedMap(obj.Object, spec, "spec"); err != nil {
			return err
		}
		if err := kube.Apply(cmd.Context(), dynamicClient, gvr, namespace, obj); err != nil {
			return err
		}
		fmt.Printf("WebApplication %s updated in namespace %s\n", name, namespace)
		return nil
	},
}

var appApplyCmd = &cobra.Command{
	Use:   "apply -f <file>",
	Short: "Apply a WebApplication manifest",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(appApplyFilename) == "" {
			return fmt.Errorf("pass a manifest with -f")
		}
		content, err := os.ReadFile(appApplyFilename)
		if err != nil {
			return err
		}
		defaultNamespace := optionalNamespace()
		if err := kube.ApplyManifest(cmd.Context(), kubeconfig, content, defaultNamespace); err != nil {
			return err
		}
		fmt.Printf("Applied manifest %s\n", appApplyFilename)
		return nil
	},
}

var appBuildImageCmd = &cobra.Command{
	Use:   "build-image <image> [context]",
	Short: "Build and load a local image into a vind cluster",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if currentConfig.Provider() != config.ProviderVind {
			return fmt.Errorf("local image loading requires cluster.provider: vind")
		}
		image := args[0]
		contextPath := "."
		if len(args) == 2 {
			contextPath = args[1]
		}
		clusterName := configuredClusterName(cmd, "cluster", appImageCluster)
		if err := bootstrap.CheckDiskHeadroom(bootstrap.ImageBuildDiskMinimum); err != nil {
			return err
		}
		if err := bootstrap.BuildLocalImage(cmd.Context(), image, contextPath); err != nil {
			return err
		}
		if err := bootstrap.LoadImageIntoVindCluster(cmd.Context(), clusterName, image); err != nil {
			return err
		}
		fmt.Printf("Image %s built and loaded into cluster %s\n", image, clusterName)
		return nil
	},
}

var appLoadImageCmd = &cobra.Command{
	Use:   "load-image <image>",
	Short: "Load a local Docker image into a vind cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if currentConfig.Provider() != config.ProviderVind {
			return fmt.Errorf("local image loading requires cluster.provider: vind")
		}
		image := args[0]
		clusterName := configuredClusterName(cmd, "cluster", appImageCluster)
		if err := bootstrap.CheckDiskHeadroom(bootstrap.ImageBuildDiskMinimum); err != nil {
			return err
		}
		if err := bootstrap.LoadImageIntoVindCluster(cmd.Context(), clusterName, image); err != nil {
			return err
		}
		fmt.Printf("Image %s loaded into cluster %s\n", image, clusterName)
		return nil
	},
}

var appListCmd = &cobra.Command{
	Use:   "list",
	Short: "List WebApplications",
	RunE: func(cmd *cobra.Command, args []string) error {
		namespace, err := currentNamespace()
		if err != nil {
			return err
		}
		format, err := outputOption()
		if err != nil {
			return err
		}
		dynamicClient, err := kube.NewDynamicClient(kubeconfig)
		if err != nil {
			return err
		}
		gvr := schema.GroupVersionResource{Group: v1alpha1.Group, Version: v1alpha1.Version, Resource: "webapplications"}
		list, err := dynamicClient.Resource(gvr).Namespace(namespace).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			return err
		}

		if format == output.Table {
			rows := [][]string{}
			for _, item := range list.Items {
				spec, _ := item.Object["spec"].(map[string]interface{})
				image := fmt.Sprintf("%v:%v", spec["image"], spec["tag"])
				host := fmt.Sprintf("%v", spec["host"])
				if host == "<nil>" || host == "" {
					host = "internal"
				}
				status := "Unknown"
				if statusMap, ok := item.Object["status"].(map[string]interface{}); ok {
					if conditions, ok := statusMap["conditions"].([]interface{}); ok && len(conditions) > 0 {
						status = fmt.Sprintf("%v", conditions[0])
					}
				}
				rows = append(rows, []string{item.GetName(), image, host, status})
			}
			return output.PrintTable([]string{"Name", "Image", "Host", "Status"}, rows)
		}

		payload, err := output.Render(list.Items, format)
		if err != nil {
			return err
		}
		fmt.Println(string(payload))
		return nil
	},
}

var appDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a WebApplication",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		namespace, err := currentNamespace()
		if err != nil {
			return err
		}
		dynamicClient, err := kube.NewDynamicClient(kubeconfig)
		if err != nil {
			return err
		}
		gvr := schema.GroupVersionResource{Group: v1alpha1.Group, Version: v1alpha1.Version, Resource: "webapplications"}
		if err := dynamicClient.Resource(gvr).Namespace(namespace).Delete(context.Background(), name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		fmt.Printf("WebApplication %s deleted in namespace %s\n", name, namespace)
		return nil
	},
}

var appDescribeCmd = &cobra.Command{
	Use:   "describe <name>",
	Short: "Describe a WebApplication",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		namespace, err := currentNamespace()
		if err != nil {
			return err
		}
		dynamicClient, err := kube.NewDynamicClient(kubeconfig)
		if err != nil {
			return err
		}
		gvr := schema.GroupVersionResource{Group: v1alpha1.Group, Version: v1alpha1.Version, Resource: "webapplications"}
		obj, err := dynamicClient.Resource(gvr).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		payload, err := output.Render(obj, output.YAML)
		if err != nil {
			return err
		}
		fmt.Println(string(payload))
		return nil
	},
}

func buildWebApplication(name, namespace string) (v1alpha1.WebApplication, error) {
	image, tag := parseImageTag(appImage, appTag)
	host := strings.TrimSpace(appHost)
	if host == "" && !appInternal {
		host = fmt.Sprintf("%s.local", name)
	}

	env, err := parseEnvVars(appEnv)
	if err != nil {
		return v1alpha1.WebApplication{}, err
	}
	fieldRefEnv, err := parseFieldRefEnv(appEnvFieldRef)
	if err != nil {
		return v1alpha1.WebApplication{}, err
	}
	envFrom := buildEnvFrom(appEnvFromConfigMaps, appEnvFromSecrets)
	secretItems, err := parseSecretItems(appSecretItems)
	if err != nil {
		return v1alpha1.WebApplication{}, err
	}
	volumes, volumeMounts, err := buildVolumesAndMounts(appSecretMounts, appEmptyDirMounts, appConfigMapMounts, secretItems)
	if err != nil {
		return v1alpha1.WebApplication{}, err
	}
	resources := buildResources()
	securityContext, err := buildSecurityContext()
	if err != nil {
		return v1alpha1.WebApplication{}, err
	}

	spec := v1alpha1.WebApplicationSpec{
		Image:              image,
		Tag:                tag,
		Replicas:           appReplicas,
		Host:               host,
		Port:               appPort,
		Command:            appCommand,
		Args:               appArgs,
		Service:            &v1alpha1.ServiceSpec{Port: appServicePort},
		Env:                append(env, fieldRefEnv...),
		EnvFrom:            envFrom,
		Volumes:            volumes,
		VolumeMounts:       volumeMounts,
		ReadinessProbe:     buildHTTPProbe(appReadinessPath, appPort),
		LivenessProbe:      buildHTTPProbe(appLivenessPath, appPort),
		StartupProbe:       buildHTTPProbe(appStartupPath, appPort),
		Resources:          resources,
		SecurityContext:    securityContext,
		PodSecurityContext: buildPodSecurityContext(),
	}
	if appInternal {
		spec.Route = &v1alpha1.RouteSpec{Enabled: boolPtr(false)}
	}

	return v1alpha1.WebApplication{
		TypeMeta:   v1alpha1.TypeMeta("WebApplication"),
		ObjectMeta: v1alpha1.ObjectMeta(name, namespace),
		Spec:       spec,
	}, nil
}

func applyWebApplication(ctx context.Context, namespace string, content []byte) error {
	obj := &unstructured.Unstructured{}
	if err := yaml.Unmarshal(content, obj); err != nil {
		return err
	}
	dynamicClient, err := kube.NewDynamicClient(kubeconfig)
	if err != nil {
		return err
	}
	gvr := schema.GroupVersionResource{Group: v1alpha1.Group, Version: v1alpha1.Version, Resource: "webapplications"}
	return kube.Apply(ctx, dynamicClient, gvr, namespace, obj)
}

func applyAppFlagOverrides(cmd *cobra.Command, name string, spec map[string]interface{}) (bool, error) {
	changed := false
	if cmd.Flags().Changed("image") {
		image, tag := parseImageTag(appImage, appTag)
		spec["image"] = image
		if !cmd.Flags().Changed("tag") {
			spec["tag"] = tag
		}
		changed = true
	}
	if cmd.Flags().Changed("tag") {
		spec["tag"] = appTag
		changed = true
	}
	if cmd.Flags().Changed("replicas") {
		spec["replicas"] = int64(appReplicas)
		changed = true
	}
	if cmd.Flags().Changed("port") {
		spec["port"] = int64(appPort)
		changed = true
	}
	if cmd.Flags().Changed("service-port") {
		spec["service"] = map[string]interface{}{"port": int64(appServicePort)}
		changed = true
	}
	if cmd.Flags().Changed("command") {
		spec["command"] = toUnstructuredStringList(appCommand)
		changed = true
	}
	if cmd.Flags().Changed("arg") {
		spec["args"] = toUnstructuredStringList(appArgs)
		changed = true
	}
	if cmd.Flags().Changed("host") {
		spec["host"] = appHost
		spec["route"] = map[string]interface{}{"enabled": true}
		changed = true
	}
	if cmd.Flags().Changed("internal") && appInternal {
		delete(spec, "host")
		spec["route"] = map[string]interface{}{"enabled": false}
		changed = true
	}
	if cmd.Flags().Changed("env") || cmd.Flags().Changed("env-fieldref") {
		env, err := parseEnvVars(appEnv)
		if err != nil {
			return false, err
		}
		fieldRefEnv, err := parseFieldRefEnv(appEnvFieldRef)
		if err != nil {
			return false, err
		}
		spec["env"] = toUnstructuredList(append(env, fieldRefEnv...))
		changed = true
	}
	if cmd.Flags().Changed("env-from-configmap") || cmd.Flags().Changed("env-from-secret") {
		spec["envFrom"] = toUnstructuredList(buildEnvFrom(appEnvFromConfigMaps, appEnvFromSecrets))
		changed = true
	}
	if cmd.Flags().Changed("secret-mount") || cmd.Flags().Changed("empty-dir") || cmd.Flags().Changed("configmap-mount") || cmd.Flags().Changed("secret-items") {
		secretItems, err := parseSecretItems(appSecretItems)
		if err != nil {
			return false, err
		}
		volumes, volumeMounts, err := buildVolumesAndMounts(appSecretMounts, appEmptyDirMounts, appConfigMapMounts, secretItems)
		if err != nil {
			return false, err
		}
		spec["volumes"] = toUnstructuredList(volumes)
		spec["volumeMounts"] = toUnstructuredList(volumeMounts)
		changed = true
	}
	if cmd.Flags().Changed("readiness-path") {
		spec["readinessProbe"] = buildHTTPProbePreservingTiming(cmd, spec, "readinessProbe", appReadinessPath, appPort)
		changed = true
	}
	if cmd.Flags().Changed("liveness-path") {
		spec["livenessProbe"] = buildHTTPProbePreservingTiming(cmd, spec, "livenessProbe", appLivenessPath, appPort)
		changed = true
	}
	if cmd.Flags().Changed("startup-path") {
		spec["startupProbe"] = buildHTTPProbePreservingTiming(cmd, spec, "startupProbe", appStartupPath, appPort)
		changed = true
	}
	if anyFlagChanged(cmd, "probe-initial-delay", "probe-period") {
		if applyProbeTiming(spec, cmd.Flags().Changed("probe-initial-delay"), cmd.Flags().Changed("probe-period")) {
			changed = true
		}
	}
	if anyFlagChanged(cmd, "cpu-request", "memory-request", "cpu-limit", "memory-limit", "ephemeral-storage-request", "ephemeral-storage-limit") {
		spec["resources"] = buildResources()
		changed = true
	}
	if cmd.Flags().Changed("fs-group") {
		if podSecurityContext := buildPodSecurityContext(); podSecurityContext != nil {
			spec["podSecurityContext"] = podSecurityContext
		} else {
			delete(spec, "podSecurityContext")
		}
		changed = true
	}
	if anyFlagChanged(cmd, "read-only-root-filesystem", "run-as-non-root", "run-as-user") {
		securityContext, err := buildSecurityContext()
		if err != nil {
			return false, err
		}
		spec["securityContext"] = securityContext
		changed = true
	}
	if cmd.Flags().Changed("internal") && !appInternal {
		host := strings.TrimSpace(appHost)
		if host == "" {
			host = fmt.Sprintf("%s.local", name)
		}
		spec["host"] = host
		spec["route"] = map[string]interface{}{"enabled": true}
		changed = true
	}
	return changed, nil
}

func parseImageTag(image, overrideTag string) (string, string) {
	if overrideTag != "" {
		return image, overrideTag
	}
	lastSlash := strings.LastIndex(image, "/")
	lastColon := strings.LastIndex(image, ":")
	if lastColon > lastSlash {
		return image[:lastColon], image[lastColon+1:]
	}
	return image, "latest"
}

func parseEnvVars(entries []string) ([]map[string]interface{}, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	env := make([]map[string]interface{}, 0, len(entries))
	for _, entry := range entries {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
			return nil, fmt.Errorf("invalid env entry %q, expected KEY=VALUE", entry)
		}
		env = append(env, map[string]interface{}{
			"name":  strings.TrimSpace(parts[0]),
			"value": parts[1],
		})
	}
	return env, nil
}

// parseFieldRefEnv parses downward-API environment entries in the form
// NAME=field.path (e.g. NAMESPACE=metadata.namespace, POD_IP=status.podIP)
// into Kubernetes EnvVar-style maps with valueFrom.fieldRef.
func parseFieldRefEnv(entries []string) ([]map[string]interface{}, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	env := make([]map[string]interface{}, 0, len(entries))
	for _, entry := range entries {
		name, fieldPath, ok := strings.Cut(entry, "=")
		name, fieldPath = strings.TrimSpace(name), strings.TrimSpace(fieldPath)
		if !ok || name == "" || fieldPath == "" {
			return nil, fmt.Errorf("invalid fieldref env entry %q, expected NAME=field.path", entry)
		}
		env = append(env, map[string]interface{}{
			"name": name,
			"valueFrom": map[string]interface{}{
				"fieldRef": map[string]interface{}{"fieldPath": fieldPath},
			},
		})
	}
	return env, nil
}

// toUnstructuredStringList converts a string slice into the []interface{}
// form required by unstructured objects ([]string panics on deep-copy, same
// as []map[string]interface{}).
func toUnstructuredStringList(items []string) []interface{} {
	if items == nil {
		return nil
	}
	out := make([]interface{}, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	return out
}

// toUnstructuredList converts a typed map slice into the []interface{}
// form required by unstructured objects. Assigning []map[string]interface{}
// directly into an unstructured map panics on deep-copy
// ("cannot deep copy []map[string]interface {}").
func toUnstructuredList(items []map[string]interface{}) []interface{} {
	if items == nil {
		return nil
	}
	out := make([]interface{}, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	return out
}

func buildEnvFrom(configMaps, secrets []string) []map[string]interface{} {
	envFrom := make([]map[string]interface{}, 0, len(configMaps)+len(secrets))
	for _, name := range configMaps {
		cleanName := strings.TrimSpace(name)
		if cleanName == "" {
			continue
		}
		envFrom = append(envFrom, map[string]interface{}{"configMapRef": map[string]interface{}{"name": cleanName}})
	}
	for _, name := range secrets {
		cleanName := strings.TrimSpace(name)
		if cleanName == "" {
			continue
		}
		envFrom = append(envFrom, map[string]interface{}{"secretRef": map[string]interface{}{"name": cleanName}})
	}
	if len(envFrom) == 0 {
		return nil
	}
	return envFrom
}

func buildVolumesAndMounts(secretMounts, emptyDirMounts, configMapMounts []string, secretItems map[string][]interface{}) ([]map[string]interface{}, []map[string]interface{}, error) {
	volumes := []map[string]interface{}{}
	volumeMounts := []map[string]interface{}{}
	for _, entry := range secretMounts {
		parts := strings.Split(entry, ":")
		if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return nil, nil, fmt.Errorf("invalid secret mount %q, expected secretName:mountPath[:volumeName]", entry)
		}
		secretName := strings.TrimSpace(parts[0])
		mountPath := strings.TrimSpace(parts[1])
		volumeName := secretName
		if len(parts) > 2 && strings.TrimSpace(parts[2]) != "" {
			volumeName = strings.TrimSpace(parts[2])
		}
		secretSource := map[string]interface{}{"secretName": secretName}
		if items, ok := secretItems[secretName]; ok {
			secretSource["items"] = items
			delete(secretItems, secretName)
		}
		volumes = append(volumes, map[string]interface{}{
			"name":   volumeName,
			"secret": secretSource,
		})
		volumeMounts = append(volumeMounts, map[string]interface{}{
			"name":      volumeName,
			"mountPath": mountPath,
			"readOnly":  true,
		})
	}
	for _, entry := range configMapMounts {
		parts := strings.Split(entry, ":")
		if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return nil, nil, fmt.Errorf("invalid configmap mount %q, expected configMapName:mountPath[:volumeName]", entry)
		}
		configMapName := strings.TrimSpace(parts[0])
		mountPath := strings.TrimSpace(parts[1])
		volumeName := configMapName
		if len(parts) > 2 && strings.TrimSpace(parts[2]) != "" {
			volumeName = strings.TrimSpace(parts[2])
		}
		volumes = append(volumes, map[string]interface{}{
			"name":      volumeName,
			"configMap": map[string]interface{}{"name": configMapName},
		})
		volumeMounts = append(volumeMounts, map[string]interface{}{
			"name":      volumeName,
			"mountPath": mountPath,
			"readOnly":  true,
		})
	}
	for _, entry := range emptyDirMounts {
		parts := strings.Split(entry, ":")
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return nil, nil, fmt.Errorf("invalid emptyDir mount %q, expected name:mountPath", entry)
		}
		volumeName := strings.TrimSpace(parts[0])
		mountPath := strings.TrimSpace(parts[1])
		volumes = append(volumes, map[string]interface{}{
			"name":     volumeName,
			"emptyDir": map[string]interface{}{},
		})
		volumeMounts = append(volumeMounts, map[string]interface{}{
			"name":      volumeName,
			"mountPath": mountPath,
		})
	}
	if len(secretItems) > 0 {
		names := make([]string, 0, len(secretItems))
		for name := range secretItems {
			names = append(names, name)
		}
		return nil, nil, fmt.Errorf("secret items for unmounted secret(s) %q: add a matching --secret-mount first", strings.Join(names, ","))
	}
	if len(volumes) == 0 {
		return nil, nil, nil
	}
	return volumes, volumeMounts, nil
}

// parseSecretItems parses key-to-path mappings for mounted secrets, in the
// form secretName:key=path[,key=path...] (repeatable). The returned items are
// unstructured-safe ([]interface{} of maps) for direct assignment into specs.
func parseSecretItems(entries []string) (map[string][]interface{}, error) {
	items := map[string][]interface{}{}
	for _, entry := range entries {
		name, rest, ok := strings.Cut(entry, ":")
		name = strings.TrimSpace(name)
		if !ok || name == "" || strings.TrimSpace(rest) == "" {
			return nil, fmt.Errorf("invalid secret items %q, expected secretName:key=path[,key=path...]", entry)
		}
		mappings := []interface{}{}
		for pair := range strings.SplitSeq(rest, ",") {
			key, path, ok := strings.Cut(pair, "=")
			key, path = strings.TrimSpace(key), strings.TrimSpace(path)
			if !ok || key == "" || path == "" {
				return nil, fmt.Errorf("invalid secret items %q, expected key=path pairs", entry)
			}
			mappings = append(mappings, map[string]interface{}{"key": key, "path": path})
		}
		items[name] = append(items[name], mappings...)
	}
	return items, nil
}

func buildHTTPProbe(path string, port int32) map[string]interface{} {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	return map[string]interface{}{
		"httpGet": map[string]interface{}{
			"path": strings.TrimSpace(path),
			"port": int64(port),
		},
		"initialDelaySeconds": int64(appProbeInitialDelay),
		"periodSeconds":       int64(appProbePeriod),
	}
}

// buildHTTPProbePreservingTiming rebuilds a probe from a new path while
// keeping previously configured timing for whichever timing flags were not
// passed. Without this, `app update --readiness-path` would silently reset a
// customized period back to the default.
func buildHTTPProbePreservingTiming(cmd *cobra.Command, spec map[string]interface{}, key, path string, port int32) map[string]interface{} {
	probe := buildHTTPProbe(path, port)
	if probe == nil {
		return nil
	}
	if prev, ok := spec[key].(map[string]interface{}); ok && prev != nil {
		if !cmd.Flags().Changed("probe-initial-delay") {
			if delay, ok := prev["initialDelaySeconds"]; ok {
				probe["initialDelaySeconds"] = delay
			}
		}
		if !cmd.Flags().Changed("probe-period") {
			if period, ok := prev["periodSeconds"]; ok {
				probe["periodSeconds"] = period
			}
		}
	}
	return probe
}

// applyProbeTiming sets the configured probe timing on every probe present in
// the spec, whether just built from flags or fetched from the server. Only
// timing fields whose flags changed are touched, so retuning the delay does
// not reset a customized period (Java services typically need 60–120s
// startup delays with short periods).
func applyProbeTiming(spec map[string]interface{}, setDelay, setPeriod bool) bool {
	changed := false
	for _, key := range []string{"readinessProbe", "livenessProbe", "startupProbe"} {
		probe, ok := spec[key].(map[string]interface{})
		if !ok || probe == nil {
			continue
		}
		if setDelay {
			probe["initialDelaySeconds"] = int64(appProbeInitialDelay)
		}
		if setPeriod {
			probe["periodSeconds"] = int64(appProbePeriod)
		}
		changed = true
	}
	return changed
}

func buildResources() map[string]interface{} {
	requests := map[string]interface{}{}
	limits := map[string]interface{}{}
	if appCPURequest != "" {
		requests["cpu"] = appCPURequest
	}
	if appMemoryRequest != "" {
		requests["memory"] = appMemoryRequest
	}
	if appEphemeralRequest != "" {
		requests["ephemeral-storage"] = appEphemeralRequest
	}
	if appCPULimit != "" {
		limits["cpu"] = appCPULimit
	}
	if appMemoryLimit != "" {
		limits["memory"] = appMemoryLimit
	}
	if appEphemeralLimit != "" {
		limits["ephemeral-storage"] = appEphemeralLimit
	}
	resources := map[string]interface{}{}
	if len(requests) > 0 {
		resources["requests"] = requests
	}
	if len(limits) > 0 {
		resources["limits"] = limits
	}
	if len(resources) == 0 {
		return nil
	}
	return resources
}

// buildPodSecurityContext returns the pod-level security context, currently
// carrying only fsGroup. Nil when unset so the field stays omitted.
func buildPodSecurityContext() map[string]interface{} {
	if appFsGroup < 0 {
		return nil
	}
	return map[string]interface{}{"fsGroup": appFsGroup}
}

func buildSecurityContext() (map[string]interface{}, error) {
	securityContext := map[string]interface{}{}
	if appReadOnlyRootFS {
		securityContext["readOnlyRootFilesystem"] = true
	}
	if appRunAsNonRoot {
		securityContext["runAsNonRoot"] = true
	}
	if appRunAsUser >= 0 {
		if appRunAsUser > 2147483647 {
			return nil, fmt.Errorf("run-as-user must fit in a Kubernetes int32")
		}
		securityContext["runAsUser"] = appRunAsUser
	}
	if len(securityContext) == 0 {
		return nil, nil
	}
	return securityContext, nil
}

func optionalNamespace() string {
	if namespaceOverride != "" {
		return namespaceOverride
	}
	if currentConfig != nil {
		return currentConfig.CurrentWorkspace
	}
	return ""
}

func anyFlagChanged(cmd *cobra.Command, names ...string) bool {
	for _, name := range names {
		if cmd.Flags().Changed(name) {
			return true
		}
	}
	return false
}

func init() {
	appCmd.AddCommand(appInitCmd)
	appCmd.AddCommand(appUpdateCmd)
	appCmd.AddCommand(appApplyCmd)
	appCmd.AddCommand(appBuildImageCmd)
	appCmd.AddCommand(appLoadImageCmd)
	appCmd.AddCommand(appListCmd)
	appCmd.AddCommand(appDeleteCmd)
	appCmd.AddCommand(appDescribeCmd)

	registerAppSpecFlags(appInitCmd, true)
	registerAppSpecFlags(appUpdateCmd, false)
	appApplyCmd.Flags().StringVarP(&appApplyFilename, "filename", "f", "", "Manifest file to apply")
	appBuildImageCmd.Flags().StringVar(&appImageCluster, "cluster", "", "Target local vind cluster name")
	appLoadImageCmd.Flags().StringVar(&appImageCluster, "cluster", "", "Target local vind cluster name")

	registerNamespaceFlag(appInitCmd)
	registerNamespaceFlag(appUpdateCmd)
	registerNamespaceFlag(appApplyCmd)
	registerNamespaceFlag(appListCmd)
	registerNamespaceFlag(appDeleteCmd)
	registerNamespaceFlag(appDescribeCmd)
}

func registerAppSpecFlags(cmd *cobra.Command, requireImage bool) {
	cmd.Flags().StringVar(&appImage, "image", "", "Container image (repo or repo:tag)")
	cmd.Flags().StringVar(&appTag, "tag", "", "Override image tag")
	cmd.Flags().StringArrayVar(&appCommand, "command", nil, "Container entrypoint command, repeatable")
	cmd.Flags().StringArrayVar(&appArgs, "arg", nil, "Container argument, repeatable")
	cmd.Flags().StringVar(&appHost, "host", "", "Hostname for HTTP routing")
	cmd.Flags().Int32Var(&appPort, "port", 80, "Container port")
	cmd.Flags().Int32Var(&appServicePort, "service-port", 80, "Kubernetes Service port")
	cmd.Flags().Int32Var(&appReplicas, "replicas", 1, "Number of replicas")
	cmd.Flags().BoolVar(&appInternal, "internal", false, "Create only an internal Service without an HTTPRoute")
	cmd.Flags().StringArrayVar(&appEnv, "env", nil, "Environment variable (KEY=VALUE), repeatable")
	cmd.Flags().StringArrayVar(&appEnvFieldRef, "env-fieldref", nil, "Downward API environment variable (NAME=field.path, e.g. NAMESPACE=metadata.namespace), repeatable")
	cmd.Flags().StringArrayVar(&appEnvFromConfigMaps, "env-from-configmap", nil, "ConfigMap to expose through envFrom, repeatable")
	cmd.Flags().StringArrayVar(&appEnvFromSecrets, "env-from-secret", nil, "Secret to expose through envFrom, repeatable")
	cmd.Flags().StringArrayVar(&appSecretMounts, "secret-mount", nil, "Mount a Secret as a volume (secretName:mountPath[:volumeName]), repeatable")
	cmd.Flags().StringArrayVar(&appConfigMapMounts, "configmap-mount", nil, "Mount a ConfigMap as a volume (configMapName:mountPath[:volumeName]), repeatable")
	cmd.Flags().StringArrayVar(&appSecretItems, "secret-items", nil, "Map Secret keys to file paths (secretName:key=path[,key=path...]), repeatable; requires a matching --secret-mount")
	cmd.Flags().StringArrayVar(&appEmptyDirMounts, "empty-dir", nil, "Mount a writable emptyDir volume (name:mountPath), repeatable")
	cmd.Flags().StringVar(&appReadinessPath, "readiness-path", "", "HTTP readiness probe path")
	cmd.Flags().StringVar(&appLivenessPath, "liveness-path", "", "HTTP liveness probe path")
	cmd.Flags().StringVar(&appStartupPath, "startup-path", "", "HTTP startup probe path")
	cmd.Flags().Int32Var(&appProbeInitialDelay, "probe-initial-delay", 5, "Initial delay in seconds for HTTP probes")
	cmd.Flags().Int32Var(&appProbePeriod, "probe-period", 10, "Period in seconds for HTTP probes")
	cmd.Flags().StringVar(&appCPURequest, "cpu-request", "", "CPU request, for example 100m")
	cmd.Flags().StringVar(&appMemoryRequest, "memory-request", "", "Memory request, for example 128Mi")
	cmd.Flags().StringVar(&appCPULimit, "cpu-limit", "", "CPU limit, for example 500m")
	cmd.Flags().StringVar(&appMemoryLimit, "memory-limit", "", "Memory limit, for example 256Mi")
	cmd.Flags().StringVar(&appEphemeralRequest, "ephemeral-storage-request", "", "Ephemeral storage request, for example 1Gi")
	cmd.Flags().StringVar(&appEphemeralLimit, "ephemeral-storage-limit", "", "Ephemeral storage limit, for example 2Gi")
	cmd.Flags().Int64Var(&appFsGroup, "fs-group", -1, "Set pod securityContext.fsGroup")
	cmd.Flags().BoolVar(&appReadOnlyRootFS, "read-only-root-filesystem", false, "Set container securityContext.readOnlyRootFilesystem")
	cmd.Flags().BoolVar(&appRunAsNonRoot, "run-as-non-root", false, "Set container securityContext.runAsNonRoot")
	cmd.Flags().Int64Var(&appRunAsUser, "run-as-user", -1, "Set container securityContext.runAsUser")
	if requireImage {
		cmd.Flags().BoolVar(&appDryRun, "dry-run", false, "Print YAML instead of applying")
		if err := cmd.MarkFlagRequired("image"); err != nil {
			panic(err)
		}
	}
}
