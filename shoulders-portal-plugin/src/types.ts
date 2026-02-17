export type ResourceItem = {
	name: string;
	namespace: string;
	createdAt: string;
	synced: boolean | null;
	ready: boolean | null;
	raw: Record<string, any>;
};

export type ResourceConfig = {
	id: string;
	label: string;
	description: string;
	path: string;
	plural: string;
	apiVersion: string;
	kind: string;
	namespaced: boolean;
};

export type CreateMode = 'form' | 'yaml';

export type CreateFormState = {
	name: string;
	namespace: string;
	webapp: {
		image: string;
		tag: string;
		replicas: string;
		host: string;
	};
	stateStore: {
		postgresEnabled: boolean;
		postgresStorage: string;
		postgresDatabases: string;
		redisEnabled: boolean;
		redisReplicas: string;
	};
	eventStream: {
		topicsText: string;
	};
};
