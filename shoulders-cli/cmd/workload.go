package cmd

import (
	"context"
	"fmt"

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
	workloadImage             string
	workloadTag               string
	workloadReplicas          int32
	workloadSchedule          string
	workloadRestartPolicy     string
	workloadBackoffLimit      int32
	workloadConcurrencyPolicy string
	workloadCommand           []string
	workloadArgs              []string
	workloadDryRun            bool
)

var workloadCmd = &cobra.Command{
	Use:   "workload",
	Short: "Manage background workers and jobs",
}

var workloadWorkerCmd = &cobra.Command{
	Use:   "worker <name>",
	Short: "Create or update a background worker",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWorkloadApply(cmd, args[0], "worker")
	},
}

var workloadJobCmd = &cobra.Command{
	Use:   "job <name>",
	Short: "Create or update a one-shot job",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWorkloadApply(cmd, args[0], "job")
	},
}

var workloadCronCmd = &cobra.Command{
	Use:   "cron <name>",
	Short: "Create or update a scheduled job",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if workloadSchedule == "" {
			return fmt.Errorf("--schedule is required for cron workloads")
		}
		return runWorkloadApply(cmd, args[0], "cronjob")
	},
}

var workloadListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Workloads",
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
		gvr := schema.GroupVersionResource{Group: v1alpha1.Group, Version: v1alpha1.Version, Resource: "workloads"}
		list, err := dynamicClient.Resource(gvr).Namespace(namespace).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			return err
		}

		if format == output.Table {
			rows := [][]string{}
			for _, item := range list.Items {
				spec, _ := item.Object["spec"].(map[string]interface{})
				workloadType := fmt.Sprintf("%v", spec["type"])
				if workloadType == "<nil>" || workloadType == "" {
					workloadType = "worker"
				}
				image := fmt.Sprintf("%v:%v", spec["image"], spec["tag"])
				status := "Unknown"
				if statusMap, ok := item.Object["status"].(map[string]interface{}); ok {
					if conditions, ok := statusMap["conditions"].([]interface{}); ok && len(conditions) > 0 {
						status = fmt.Sprintf("%v", conditions[0])
					}
				}
				rows = append(rows, []string{item.GetName(), workloadType, image, status})
			}
			return output.PrintTable([]string{"Name", "Type", "Image", "Status"}, rows)
		}

		payload, err := output.Render(list.Items, format)
		if err != nil {
			return err
		}
		fmt.Println(string(payload))
		return nil
	},
}

var workloadDescribeCmd = &cobra.Command{
	Use:   "describe <name>",
	Short: "Describe a Workload",
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
		gvr := schema.GroupVersionResource{Group: v1alpha1.Group, Version: v1alpha1.Version, Resource: "workloads"}
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

var workloadDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a Workload",
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
		gvr := schema.GroupVersionResource{Group: v1alpha1.Group, Version: v1alpha1.Version, Resource: "workloads"}
		if err := dynamicClient.Resource(gvr).Namespace(namespace).Delete(context.Background(), name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		fmt.Printf("Workload %s deleted in namespace %s\n", name, namespace)
		return nil
	},
}

func runWorkloadApply(cmd *cobra.Command, name, workloadType string) error {
	namespace, err := currentNamespace()
	if err != nil {
		return err
	}
	workload, err := buildWorkload(name, namespace, workloadType)
	if err != nil {
		return err
	}
	manifest, err := yaml.Marshal(workload)
	if err != nil {
		return err
	}
	if workloadDryRun {
		fmt.Println(string(manifest))
		return nil
	}
	obj := &unstructured.Unstructured{}
	if err := yaml.Unmarshal(manifest, obj); err != nil {
		return err
	}
	dynamicClient, err := kube.NewDynamicClient(kubeconfig)
	if err != nil {
		return err
	}
	gvr := schema.GroupVersionResource{Group: v1alpha1.Group, Version: v1alpha1.Version, Resource: "workloads"}
	if err := kube.Apply(cmd.Context(), dynamicClient, gvr, namespace, obj); err != nil {
		return err
	}
	fmt.Printf("Workload %s applied in namespace %s\n", name, namespace)
	return nil
}

func buildWorkload(name, namespace, workloadType string) (v1alpha1.Workload, error) {
	image, tag := parseImageTag(workloadImage, workloadTag)
	env, err := parseEnvVars(appEnv)
	if err != nil {
		return v1alpha1.Workload{}, err
	}
	resources := buildResources()
	securityContext, err := buildSecurityContext()
	if err != nil {
		return v1alpha1.Workload{}, err
	}

	spec := v1alpha1.WorkloadSpec{
		Type:              workloadType,
		Image:             image,
		Tag:               tag,
		Replicas:          workloadReplicas,
		Schedule:          workloadSchedule,
		RestartPolicy:     workloadRestartPolicy,
		BackoffLimit:      int32Ptr(workloadBackoffLimit),
		ConcurrencyPolicy: workloadConcurrencyPolicy,
		Command:           workloadCommand,
		Args:              workloadArgs,
		Env:               env,
		EnvFrom:           buildEnvFrom(appEnvFromConfigMaps, appEnvFromSecrets),
		Resources:         resources,
		SecurityContext:   securityContext,
	}

	return v1alpha1.Workload{
		TypeMeta:   v1alpha1.TypeMeta("Workload"),
		ObjectMeta: v1alpha1.ObjectMeta(name, namespace),
		Spec:       spec,
	}, nil
}

func init() {
	workloadCmd.AddCommand(workloadWorkerCmd)
	workloadCmd.AddCommand(workloadJobCmd)
	workloadCmd.AddCommand(workloadCronCmd)
	workloadCmd.AddCommand(workloadListCmd)
	workloadCmd.AddCommand(workloadDescribeCmd)
	workloadCmd.AddCommand(workloadDeleteCmd)

	registerWorkloadCreateFlags(workloadWorkerCmd)
	registerWorkloadCreateFlags(workloadJobCmd)
	registerWorkloadCreateFlags(workloadCronCmd)

	registerNamespaceFlag(workloadWorkerCmd)
	registerNamespaceFlag(workloadJobCmd)
	registerNamespaceFlag(workloadCronCmd)
	registerNamespaceFlag(workloadListCmd)
	registerNamespaceFlag(workloadDescribeCmd)
	registerNamespaceFlag(workloadDeleteCmd)
}

func registerWorkloadCreateFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&workloadImage, "image", "", "Container image (repo or repo:tag)")
	cmd.Flags().StringVar(&workloadTag, "tag", "", "Override image tag")
	cmd.Flags().Int32Var(&workloadReplicas, "replicas", 1, "Number of worker replicas")
	cmd.Flags().StringVar(&workloadSchedule, "schedule", "", "Cron schedule, for example '*/5 * * * *'")
	cmd.Flags().StringVar(&workloadRestartPolicy, "restart-policy", "OnFailure", "Pod restart policy: Always|OnFailure|Never")
	cmd.Flags().Int32Var(&workloadBackoffLimit, "backoff-limit", 6, "Job retry backoff limit")
	cmd.Flags().StringVar(&workloadConcurrencyPolicy, "concurrency-policy", "Forbid", "Cron concurrency policy: Allow|Forbid|Replace")
	cmd.Flags().StringArrayVar(&workloadCommand, "command", nil, "Container command entry, repeatable")
	cmd.Flags().StringArrayVar(&workloadArgs, "arg", nil, "Container argument, repeatable")
	cmd.Flags().StringArrayVar(&appEnv, "env", nil, "Environment variable (KEY=VALUE), repeatable")
	cmd.Flags().StringArrayVar(&appEnvFromConfigMaps, "env-from-configmap", nil, "ConfigMap to expose through envFrom, repeatable")
	cmd.Flags().StringArrayVar(&appEnvFromSecrets, "env-from-secret", nil, "Secret to expose through envFrom, repeatable")
	cmd.Flags().StringVar(&appCPURequest, "cpu-request", "", "CPU request, for example 100m")
	cmd.Flags().StringVar(&appMemoryRequest, "memory-request", "", "Memory request, for example 128Mi")
	cmd.Flags().StringVar(&appCPULimit, "cpu-limit", "", "CPU limit, for example 500m")
	cmd.Flags().StringVar(&appMemoryLimit, "memory-limit", "", "Memory limit, for example 256Mi")
	cmd.Flags().BoolVar(&appReadOnlyRootFS, "read-only-root-filesystem", false, "Set container securityContext.readOnlyRootFilesystem")
	cmd.Flags().BoolVar(&appRunAsNonRoot, "run-as-non-root", false, "Set container securityContext.runAsNonRoot")
	cmd.Flags().Int64Var(&appRunAsUser, "run-as-user", -1, "Set container securityContext.runAsUser")
	cmd.Flags().BoolVar(&workloadDryRun, "dry-run", false, "Print YAML instead of applying")
	if err := cmd.MarkFlagRequired("image"); err != nil {
		panic(err)
	}
}
