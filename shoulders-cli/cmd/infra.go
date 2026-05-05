package cmd

import (
	"context"
	"fmt"
	"strings"

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
	dbType           string
	dbTier           string
	dbDatabase       string
	dbDatabases      string
	dbSecretName     string
	dbInitSQL        []string
	bucketName       string
	bucketSecretName string
	bucketRead       bool
	bucketWrite      bool
	bucketOwner      bool
	streamTopics     string
	streamPartitions int32
	streamReplicas   int32
	streamConfig     []string
)

var infraCmd = &cobra.Command{
	Use:   "infra",
	Short: "Provision infrastructure services",
}

var infraAddDbCmd = &cobra.Command{
	Use:   "add-db <name>",
	Short: "Create a StateStore",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		namespace, err := currentNamespace()
		if err != nil {
			return err
		}
		storage := "1Gi"
		if strings.EqualFold(dbTier, "prod") {
			storage = "10Gi"
		}
		postgresEnabled := strings.EqualFold(dbType, "postgres") || strings.EqualFold(dbType, "postgresql")
		redisEnabled := strings.EqualFold(dbType, "redis")
		if !postgresEnabled && !redisEnabled {
			return fmt.Errorf("unsupported database type: %s", dbType)
		}

		databases := []string{name}
		if cmd.Flags().Changed("databases") {
			databases = parseList(dbDatabases)
		}

		app := v1alpha1.StateStore{
			TypeMeta:   v1alpha1.TypeMeta("StateStore"),
			ObjectMeta: v1alpha1.ObjectMeta(name, namespace),
			Spec: v1alpha1.StateStoreSpec{
				Postgresql: &v1alpha1.PostgresSpec{
					Enabled:    boolPtr(postgresEnabled),
					Storage:    storage,
					Database:   dbDatabase,
					SecretName: dbSecretName,
					Databases:  databases,
					InitSQL:    append([]string(nil), dbInitSQL...),
				},
				Redis: &v1alpha1.RedisSpec{
					Enabled:  boolPtr(redisEnabled),
					Replicas: int32Ptr(1),
				},
			},
		}

		manifest, err := yaml.Marshal(app)
		if err != nil {
			return err
		}
		obj := &unstructured.Unstructured{}
		if err := yaml.Unmarshal(manifest, obj); err != nil {
			return err
		}

		dynamicClient, err := kube.NewDynamicClient(kubeconfig)
		if err != nil {
			return err
		}
		gvr := schema.GroupVersionResource{Group: v1alpha1.Group, Version: v1alpha1.Version, Resource: "statestores"}
		if err := kube.Apply(context.Background(), dynamicClient, gvr, namespace, obj); err != nil {
			return err
		}
		fmt.Printf("StateStore %s created\n", name)
		return nil
	},
}

var infraAddStreamCmd = &cobra.Command{
	Use:   "add-stream <name>",
	Short: "Create an EventStream",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !currentConfig.ProfileSpec().EventStreams {
			return fmt.Errorf("event streams require platform.profile: medium or large")
		}

		name := args[0]
		namespace, err := currentNamespace()
		if err != nil {
			return err
		}
		configMap, err := parseConfig(streamConfig)
		if err != nil {
			return err
		}
		topics := []v1alpha1.EventTopic{}
		for _, topic := range strings.Split(streamTopics, ",") {
			clean := strings.TrimSpace(topic)
			if clean == "" {
				continue
			}
			topicSpec := v1alpha1.EventTopic{Name: clean}
			if streamPartitions > 0 {
				topicSpec.Partitions = int32Ptr(streamPartitions)
			}
			if streamReplicas > 0 {
				topicSpec.Replicas = int32Ptr(streamReplicas)
			}
			if len(configMap) > 0 {
				topicSpec.Config = configMap
			}
			topics = append(topics, topicSpec)
		}
		if len(topics) == 0 {
			return fmt.Errorf("no topics provided; use --topics")
		}

		stream := v1alpha1.EventStream{
			TypeMeta:   v1alpha1.TypeMeta("EventStream"),
			ObjectMeta: v1alpha1.ObjectMeta(name, namespace),
			Spec:       v1alpha1.EventStreamSpec{Topics: topics},
		}

		manifest, err := yaml.Marshal(stream)
		if err != nil {
			return err
		}
		obj := &unstructured.Unstructured{}
		if err := yaml.Unmarshal(manifest, obj); err != nil {
			return err
		}

		dynamicClient, err := kube.NewDynamicClient(kubeconfig)
		if err != nil {
			return err
		}
		gvr := schema.GroupVersionResource{Group: v1alpha1.Group, Version: v1alpha1.Version, Resource: "eventstreams"}
		if err := kube.Apply(context.Background(), dynamicClient, gvr, namespace, obj); err != nil {
			return err
		}
		fmt.Printf("EventStream %s created\n", name)
		return nil
	},
}

var infraAddBucketCmd = &cobra.Command{
	Use:   "add-bucket <name>",
	Short: "Create an object storage bucket StateStore",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		namespace, err := currentNamespace()
		if err != nil {
			return err
		}

		bucket := strings.TrimSpace(bucketName)
		if bucket == "" {
			bucket = name
		}
		secretName := strings.TrimSpace(bucketSecretName)
		if secretName == "" {
			secretName = fmt.Sprintf("%s-s3", bucket)
		}

		store := v1alpha1.StateStore{
			TypeMeta:   v1alpha1.TypeMeta("StateStore"),
			ObjectMeta: v1alpha1.ObjectMeta(name, namespace),
			Spec: v1alpha1.StateStoreSpec{
				Postgresql: &v1alpha1.PostgresSpec{Enabled: boolPtr(false)},
				Redis:      &v1alpha1.RedisSpec{Enabled: boolPtr(false)},
				ObjectStorage: &v1alpha1.ObjectStorageSpec{
					Enabled: boolPtr(true),
					Buckets: []v1alpha1.BucketSpec{
						{
							Name:       bucket,
							SecretName: secretName,
							Read:       boolPtr(bucketRead),
							Write:      boolPtr(bucketWrite),
							Owner:      boolPtr(bucketOwner),
						},
					},
				},
			},
		}

		manifest, err := yaml.Marshal(store)
		if err != nil {
			return err
		}
		obj := &unstructured.Unstructured{}
		if err := yaml.Unmarshal(manifest, obj); err != nil {
			return err
		}

		dynamicClient, err := kube.NewDynamicClient(kubeconfig)
		if err != nil {
			return err
		}
		gvr := schema.GroupVersionResource{Group: v1alpha1.Group, Version: v1alpha1.Version, Resource: "statestores"}
		if err := kube.Apply(context.Background(), dynamicClient, gvr, namespace, obj); err != nil {
			return err
		}
		fmt.Printf("Object bucket %s created\n", bucket)
		return nil
	},
}

var infraListCmd = &cobra.Command{
	Use:   "list",
	Short: "List infrastructure resources",
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

		gvrSS := schema.GroupVersionResource{Group: v1alpha1.Group, Version: v1alpha1.Version, Resource: "statestores"}
		listSS, err := dynamicClient.Resource(gvrSS).Namespace(namespace).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			if isMissingAPIResource(err) {
				listSS = &unstructured.UnstructuredList{}
			} else {
				return err
			}
		}

		listES := &unstructured.UnstructuredList{}
		if currentConfig.ProfileSpec().EventStreams {
			gvrES := schema.GroupVersionResource{Group: v1alpha1.Group, Version: v1alpha1.Version, Resource: "eventstreams"}
			listES, err = dynamicClient.Resource(gvrES).Namespace(namespace).List(context.Background(), metav1.ListOptions{})
			if err != nil {
				if isMissingAPIResource(err) {
					listES = &unstructured.UnstructuredList{}
				} else {
					return err
				}
			}
		}

		if format == output.Table {
			rows := [][]string{}
			for _, item := range listSS.Items {
				rows = append(rows, []string{item.GetName(), "StateStore"})
			}
			for _, item := range listES.Items {
				rows = append(rows, []string{item.GetName(), "EventStream"})
			}
			return output.PrintTable([]string{"Name", "Kind"}, rows)
		}

		items := append(listSS.Items, listES.Items...)
		payload, err := output.Render(items, format)
		if err != nil {
			return err
		}
		fmt.Println(string(payload))
		return nil
	},
}

var infraDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete an infrastructure resource",
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

		deleted := false
		var errs []error

		gvrSS := schema.GroupVersionResource{Group: v1alpha1.Group, Version: v1alpha1.Version, Resource: "statestores"}
		err = dynamicClient.Resource(gvrSS).Namespace(namespace).Delete(context.Background(), name, metav1.DeleteOptions{})
		if err == nil {
			fmt.Printf("StateStore %s deleted\n", name)
			deleted = true
		} else if !isMissingAPIResource(err) {
			errs = append(errs, err)
		}

		if currentConfig.ProfileSpec().EventStreams {
			gvrES := schema.GroupVersionResource{Group: v1alpha1.Group, Version: v1alpha1.Version, Resource: "eventstreams"}
			err = dynamicClient.Resource(gvrES).Namespace(namespace).Delete(context.Background(), name, metav1.DeleteOptions{})
			if err == nil {
				fmt.Printf("EventStream %s deleted\n", name)
				deleted = true
			} else if !isMissingAPIResource(err) {
				errs = append(errs, err)
			}
		}

		if len(errs) > 0 {
			return fmt.Errorf("errors deleting resources: %v", errs)
		}
		if !deleted {
			return fmt.Errorf("infrastructure resource %s not found", name)
		}
		return nil
	},
}

func boolPtr(value bool) *bool {
	return &value
}

func int32Ptr(value int32) *int32 {
	return &value
}

func parseConfig(entries []string) (map[string]interface{}, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	config := make(map[string]interface{})
	for _, entry := range entries {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid config entry %q, expected key=value", entry)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" {
			return nil, fmt.Errorf("invalid config entry %q, empty key", entry)
		}
		config[key] = value
	}
	return config, nil
}

func parseList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.FieldsFunc(value, func(separator rune) bool {
		return separator == ',' || separator == '\n'
	})
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		clean := strings.TrimSpace(part)
		if clean != "" {
			items = append(items, clean)
		}
	}
	return items
}

func isMissingAPIResource(err error) bool {
	if err == nil {
		return false
	}
	if apierrors.IsNotFound(err) {
		return true
	}
	message := err.Error()
	return strings.Contains(message, "the server could not find the requested resource") || strings.Contains(message, "no matches for kind")
}

func init() {
	infraCmd.AddCommand(infraAddDbCmd)
	infraCmd.AddCommand(infraAddBucketCmd)
	infraCmd.AddCommand(infraAddStreamCmd)
	infraCmd.AddCommand(infraListCmd)
	infraCmd.AddCommand(infraDeleteCmd)

	infraAddDbCmd.Flags().StringVar(&dbType, "type", "postgres", "Database type: postgres|redis")
	infraAddDbCmd.Flags().StringVar(&dbTier, "tier", "dev", "Database tier: dev|prod")
	infraAddDbCmd.Flags().StringVar(&dbDatabase, "database", "app", "Bootstrap PostgreSQL database name")
	infraAddDbCmd.Flags().StringVar(&dbDatabases, "databases", "", "Additional PostgreSQL databases, comma or newline separated")
	infraAddDbCmd.Flags().StringVar(&dbSecretName, "secret", "", "PostgreSQL credentials Secret name (defaults to <name>-app-secret)")
	infraAddDbCmd.Flags().StringArrayVar(&dbInitSQL, "init-sql", nil, "SQL statement to run during PostgreSQL bootstrap, repeatable")
	infraAddBucketCmd.Flags().StringVar(&bucketName, "bucket", "", "Bucket name (defaults to resource name)")
	infraAddBucketCmd.Flags().StringVar(&bucketSecretName, "secret", "", "Secret name for S3 credentials (defaults to <bucket>-s3)")
	infraAddBucketCmd.Flags().BoolVar(&bucketRead, "read", true, "Grant read access to the generated key")
	infraAddBucketCmd.Flags().BoolVar(&bucketWrite, "write", true, "Grant write access to the generated key")
	infraAddBucketCmd.Flags().BoolVar(&bucketOwner, "owner", false, "Grant owner access to the generated key")

	infraAddStreamCmd.Flags().StringVar(&streamTopics, "topics", "", "Comma-separated topic names")
	infraAddStreamCmd.Flags().Int32Var(&streamPartitions, "partitions", 0, "Partitions per topic (default from XRD)")
	infraAddStreamCmd.Flags().Int32Var(&streamReplicas, "replicas", 0, "Replicas per topic (default from XRD)")
	infraAddStreamCmd.Flags().StringArrayVar(&streamConfig, "topic-config", nil, "Topic config entry (key=value), repeatable")

	registerNamespaceFlag(infraAddDbCmd)
	registerNamespaceFlag(infraAddBucketCmd)
	registerNamespaceFlag(infraAddStreamCmd)
	registerNamespaceFlag(infraListCmd)
	registerNamespaceFlag(infraDeleteCmd)
}
