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
	Postgresql *PostgresSpec `json:"postgresql,omitempty"`
	Redis      *RedisSpec    `json:"redis,omitempty"`
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
	out := new(WebApplication)
	*out = *in
	out.ObjectMeta = *in.DeepCopy()
	return out
}

func (in *WebApplicationList) DeepCopyObject() runtime.Object {
	out := new(WebApplicationList)
	*out = *in
	return out
}

func (in *StateStore) DeepCopyObject() runtime.Object {
	out := new(StateStore)
	*out = *in
	out.ObjectMeta = *in.DeepCopy()
	return out
}

func (in *StateStoreList) DeepCopyObject() runtime.Object {
	out := new(StateStoreList)
	*out = *in
	return out
}

func (in *EventStream) DeepCopyObject() runtime.Object {
	out := new(EventStream)
	*out = *in
	out.ObjectMeta = *in.DeepCopy()
	return out
}

func (in *EventStreamList) DeepCopyObject() runtime.Object {
	out := new(EventStreamList)
	*out = *in
	return out
}

func (in *Workspace) DeepCopyObject() runtime.Object {
	out := new(Workspace)
	*out = *in
	out.ObjectMeta = *in.DeepCopy()
	return out
}

func (in *WorkspaceList) DeepCopyObject() runtime.Object {
	out := new(WorkspaceList)
	*out = *in
	return out
}
