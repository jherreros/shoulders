package v1alpha1

import (
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	Group   = "shoulders.io"
	Version = "v1alpha1"
)

type WebApplication struct {
	v1.TypeMeta   `json:",inline"`
	v1.ObjectMeta `json:"metadata,omitempty"`
	Spec          WebApplicationSpec `json:"spec,omitempty"`
}

type WebApplicationSpec struct {
	Image    string `json:"image"`
	Tag      string `json:"tag"`
	Replicas int32  `json:"replicas"`
	Host     string `json:"host"`
}

type WebApplicationList struct {
	v1.TypeMeta `json:",inline"`
	v1.ListMeta `json:"metadata,omitempty"`
	Items       []WebApplication `json:"items"`
}

type StateStore struct {
	v1.TypeMeta   `json:",inline"`
	v1.ObjectMeta `json:"metadata,omitempty"`
	Spec          StateStoreSpec `json:"spec,omitempty"`
}

type StateStoreSpec struct {
	Postgresql    *PostgresSpec      `json:"postgresql,omitempty"`
	Redis         *RedisSpec         `json:"redis,omitempty"`
	ObjectStorage *ObjectStorageSpec `json:"objectStorage,omitempty"`
}

type PostgresSpec struct {
	Enabled   *bool    `json:"enabled,omitempty"`
	Storage   string   `json:"storage,omitempty"`
	Databases []string `json:"databases,omitempty"`
}

type RedisSpec struct {
	Enabled  *bool  `json:"enabled,omitempty"`
	Replicas *int32 `json:"replicas,omitempty"`
}

type ObjectStorageSpec struct {
	Enabled  *bool        `json:"enabled,omitempty"`
	Endpoint string       `json:"endpoint,omitempty"`
	Region   string       `json:"region,omitempty"`
	Buckets  []BucketSpec `json:"buckets,omitempty"`
}

type BucketSpec struct {
	Name       string `json:"name"`
	SecretName string `json:"secretName,omitempty"`
	Read       *bool  `json:"read,omitempty"`
	Write      *bool  `json:"write,omitempty"`
	Owner      *bool  `json:"owner,omitempty"`
}

type StateStoreList struct {
	v1.TypeMeta `json:",inline"`
	v1.ListMeta `json:"metadata,omitempty"`
	Items       []StateStore `json:"items"`
}

type EventStream struct {
	v1.TypeMeta   `json:",inline"`
	v1.ObjectMeta `json:"metadata,omitempty"`
	Spec          EventStreamSpec `json:"spec,omitempty"`
}

type EventStreamSpec struct {
	Topics []EventTopic `json:"topics,omitempty"`
}

type EventTopic struct {
	Name       string                 `json:"name"`
	Partitions *int32                 `json:"partitions,omitempty"`
	Replicas   *int32                 `json:"replicas,omitempty"`
	Config     map[string]interface{} `json:"config,omitempty"`
}

type EventStreamList struct {
	v1.TypeMeta `json:",inline"`
	v1.ListMeta `json:"metadata,omitempty"`
	Items       []EventStream `json:"items"`
}

type Workspace struct {
	v1.TypeMeta   `json:",inline"`
	v1.ObjectMeta `json:"metadata,omitempty"`
	Spec          WorkspaceSpec `json:"spec,omitempty"`
}

type WorkspaceSpec struct{}

type WorkspaceList struct {
	v1.TypeMeta `json:",inline"`
	v1.ListMeta `json:"metadata,omitempty"`
	Items       []Workspace `json:"items"`
}

func (in *WebApplication) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}

	out := new(WebApplication)
	*out = *in
	copyObjectMeta(&in.ObjectMeta, &out.ObjectMeta)
	return out
}

func (in *WebApplicationList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}

	out := new(WebApplicationList)
	*out = *in
	copyListMeta(&in.ListMeta, &out.ListMeta)
	if in.Items != nil {
		out.Items = make([]WebApplication, len(in.Items))
		for index := range in.Items {
			out.Items[index] = *copyWebApplication(&in.Items[index])
		}
	}
	return out
}

func (in *StateStore) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}

	out := new(StateStore)
	*out = *in
	copyObjectMeta(&in.ObjectMeta, &out.ObjectMeta)
	out.Spec = copyStateStoreSpec(in.Spec)
	return out
}

func (in *StateStoreList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}

	out := new(StateStoreList)
	*out = *in
	copyListMeta(&in.ListMeta, &out.ListMeta)
	if in.Items != nil {
		out.Items = make([]StateStore, len(in.Items))
		for index := range in.Items {
			out.Items[index] = *copyStateStore(&in.Items[index])
		}
	}
	return out
}

func (in *EventStream) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}

	out := new(EventStream)
	*out = *in
	copyObjectMeta(&in.ObjectMeta, &out.ObjectMeta)
	out.Spec = copyEventStreamSpec(in.Spec)
	return out
}

func (in *EventStreamList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}

	out := new(EventStreamList)
	*out = *in
	copyListMeta(&in.ListMeta, &out.ListMeta)
	if in.Items != nil {
		out.Items = make([]EventStream, len(in.Items))
		for index := range in.Items {
			out.Items[index] = *copyEventStream(&in.Items[index])
		}
	}
	return out
}

func (in *Workspace) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}

	out := new(Workspace)
	*out = *in
	copyObjectMeta(&in.ObjectMeta, &out.ObjectMeta)
	return out
}

func (in *WorkspaceList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}

	out := new(WorkspaceList)
	*out = *in
	copyListMeta(&in.ListMeta, &out.ListMeta)
	if in.Items != nil {
		out.Items = make([]Workspace, len(in.Items))
		for index := range in.Items {
			out.Items[index] = *copyWorkspace(&in.Items[index])
		}
	}
	return out
}

func copyWebApplication(in *WebApplication) *WebApplication {
	out := new(WebApplication)
	*out = *in
	copyObjectMeta(&in.ObjectMeta, &out.ObjectMeta)
	return out
}

func copyStateStore(in *StateStore) *StateStore {
	out := new(StateStore)
	*out = *in
	copyObjectMeta(&in.ObjectMeta, &out.ObjectMeta)
	out.Spec = copyStateStoreSpec(in.Spec)
	return out
}

func copyStateStoreSpec(in StateStoreSpec) StateStoreSpec {
	out := in
	if in.Postgresql != nil {
		out.Postgresql = &PostgresSpec{
			Enabled: copyBoolPointer(in.Postgresql.Enabled),
			Storage: in.Postgresql.Storage,
		}
		if in.Postgresql.Databases != nil {
			out.Postgresql.Databases = append([]string(nil), in.Postgresql.Databases...)
		}
	}
	if in.Redis != nil {
		out.Redis = &RedisSpec{
			Enabled:  copyBoolPointer(in.Redis.Enabled),
			Replicas: copyInt32Pointer(in.Redis.Replicas),
		}
	}
	if in.ObjectStorage != nil {
		out.ObjectStorage = &ObjectStorageSpec{
			Enabled:  copyBoolPointer(in.ObjectStorage.Enabled),
			Endpoint: in.ObjectStorage.Endpoint,
			Region:   in.ObjectStorage.Region,
		}
		if in.ObjectStorage.Buckets != nil {
			out.ObjectStorage.Buckets = make([]BucketSpec, len(in.ObjectStorage.Buckets))
			for index, bucket := range in.ObjectStorage.Buckets {
				out.ObjectStorage.Buckets[index] = BucketSpec{
					Name:       bucket.Name,
					SecretName: bucket.SecretName,
					Read:       copyBoolPointer(bucket.Read),
					Write:      copyBoolPointer(bucket.Write),
					Owner:      copyBoolPointer(bucket.Owner),
				}
			}
		}
	}
	return out
}

func copyEventStream(in *EventStream) *EventStream {
	out := new(EventStream)
	*out = *in
	copyObjectMeta(&in.ObjectMeta, &out.ObjectMeta)
	out.Spec = copyEventStreamSpec(in.Spec)
	return out
}

func copyEventStreamSpec(in EventStreamSpec) EventStreamSpec {
	out := in
	if in.Topics != nil {
		out.Topics = make([]EventTopic, len(in.Topics))
		for index, topic := range in.Topics {
			out.Topics[index] = EventTopic{
				Name:       topic.Name,
				Partitions: copyInt32Pointer(topic.Partitions),
				Replicas:   copyInt32Pointer(topic.Replicas),
				Config:     copyConfig(topic.Config),
			}
		}
	}
	return out
}

func copyWorkspace(in *Workspace) *Workspace {
	out := new(Workspace)
	*out = *in
	copyObjectMeta(&in.ObjectMeta, &out.ObjectMeta)
	return out
}

func copyObjectMeta(in *v1.ObjectMeta, out *v1.ObjectMeta) {
	in.DeepCopyInto(out)
}

func copyListMeta(in *v1.ListMeta, out *v1.ListMeta) {
	in.DeepCopyInto(out)
}

func copyConfig(in map[string]interface{}) map[string]interface{} {
	if in == nil {
		return nil
	}

	out := make(map[string]interface{}, len(in))
	for key, value := range in {
		out[key] = copyConfigValue(value)
	}
	return out
}

func copyConfigValue(value interface{}) interface{} {
	switch typedValue := value.(type) {
	case map[string]interface{}:
		return copyConfig(typedValue)
	case []interface{}:
		out := make([]interface{}, len(typedValue))
		for index := range typedValue {
			out[index] = copyConfigValue(typedValue[index])
		}
		return out
	default:
		return typedValue
	}
}

func copyBoolPointer(in *bool) *bool {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func copyInt32Pointer(in *int32) *int32 {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}
