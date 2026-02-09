package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	"github.com/jherreros/shoulders/shoulders-cli/internal/output"
	"github.com/jherreros/shoulders/shoulders-cli/pkg/api/v1alpha1"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"
)

var (
	appImage    string
	appTag      string
	appHost     string
	appPort     int
	appReplicas int32
	appDryRun   bool
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

		image, tag := parseImageTag(appImage, appTag)
		if appHost == "" {
			appHost = fmt.Sprintf("%s.local", name)
		}
		app := v1alpha1.WebApplication{
			TypeMeta:   v1alpha1.TypeMeta("WebApplication"),
			ObjectMeta: v1alpha1.ObjectMeta(name, namespace),
			Spec: v1alpha1.WebApplicationSpec{
				Image:    image,
				Tag:      tag,
				Replicas: appReplicas,
				Host:     appHost,
			},
		}
		if appPort > 0 {
			if app.Annotations == nil {
				app.Annotations = map[string]string{}
			}
			app.Annotations["shoulders.io/port"] = fmt.Sprintf("%d", appPort)
		}

		yamlBytes, err := yaml.Marshal(app)
		if err != nil {
			return err
		}
		if appDryRun {
			fmt.Println(string(yamlBytes))
			return nil
		}

		obj := &unstructured.Unstructured{}
		if err := yaml.Unmarshal(yamlBytes, obj); err != nil {
			return err
		}
		dynamicClient, err := kube.NewDynamicClient(kubeconfig)
		if err != nil {
			return err
		}

		gvr := schema.GroupVersionResource{Group: v1alpha1.Group, Version: v1alpha1.Version, Resource: "webapplications"}
		if err := kube.Apply(context.Background(), dynamicClient, gvr, namespace, obj); err != nil {
			return err
		}
		fmt.Printf("WebApplication %s created\n", name)
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
		if err := dynamicClient.Resource(gvr).Namespace(namespace).Delete(context.Background(), name, metav1.DeleteOptions{}); err != nil {
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

func parseImageTag(image, overrideTag string) (string, string) {
	if overrideTag != "" {
		return image, overrideTag
	}
	parts := strings.Split(image, ":")
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return image, "latest"
}

func init() {
	appCmd.AddCommand(appInitCmd)
	appCmd.AddCommand(appListCmd)
	appCmd.AddCommand(appDeleteCmd)
	appCmd.AddCommand(appDescribeCmd)

	appInitCmd.Flags().StringVar(&appImage, "image", "", "Container image (repo or repo:tag)")
	appInitCmd.Flags().StringVar(&appTag, "tag", "", "Override image tag")
	appInitCmd.Flags().StringVar(&appHost, "host", "", "Hostname for routing")
	appInitCmd.Flags().IntVar(&appPort, "port", 80, "Service port (stored as annotation)")
	appInitCmd.Flags().Int32Var(&appReplicas, "replicas", 1, "Number of replicas")
	appInitCmd.Flags().BoolVar(&appDryRun, "dry-run", false, "Print YAML instead of applying")
	if err := appInitCmd.MarkFlagRequired("image"); err != nil {
		panic(err)
	}

	registerNamespaceFlag(appInitCmd)
	registerNamespaceFlag(appListCmd)
	registerNamespaceFlag(appDeleteCmd)
	registerNamespaceFlag(appDescribeCmd)
}
