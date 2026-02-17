import { ResourceConfig } from './types';

export const resourceConfigs: ResourceConfig[] = [
	{
		id: 'workspaces',
		label: 'Workspaces',
		description: 'Cluster-scoped workspace foundations and guardrails.',
		path: '/apis/shoulders.io/v1alpha1/workspaces',
		plural: 'workspaces',
		apiVersion: 'shoulders.io/v1alpha1',
		kind: 'Workspace',
		namespaced: false,
	},
	{
		id: 'webapplications',
		label: 'Web Applications',
		description: 'Deployments with ingress, routing, and scaling.',
		path: '/apis/shoulders.io/v1alpha1/webapplications',
		plural: 'webapplications',
		apiVersion: 'shoulders.io/v1alpha1',
		kind: 'WebApplication',
		namespaced: true,
	},
	{
		id: 'statestores',
		label: 'State Stores',
		description: 'PostgreSQL and Redis services for teams.',
		path: '/apis/shoulders.io/v1alpha1/statestores',
		plural: 'statestores',
		apiVersion: 'shoulders.io/v1alpha1',
		kind: 'StateStore',
		namespaced: true,
	},
	{
		id: 'eventstreams',
		label: 'Event Streams',
		description: 'Kafka-backed topic bundles for streaming workloads.',
		path: '/apis/shoulders.io/v1alpha1/eventstreams',
		plural: 'eventstreams',
		apiVersion: 'shoulders.io/v1alpha1',
		kind: 'EventStream',
		namespaced: true,
	},
];
