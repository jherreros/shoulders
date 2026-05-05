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
		port: string;
		internal: boolean;
		envText: string;
	};
	workload: {
		type: string;
		image: string;
		tag: string;
		replicas: string;
		schedule: string;
		commandText: string;
		argsText: string;
		envText: string;
	};
	stateStore: {
		postgresEnabled: boolean;
		postgresStorage: string;
		postgresDatabase: string;
		postgresSecretName: string;
		postgresDatabases: string;
		postgresInitSQL: string;
		redisEnabled: boolean;
		redisReplicas: string;
		objectStorageEnabled: boolean;
		objectBuckets: string;
		objectRead: boolean;
		objectWrite: boolean;
		objectOwner: boolean;
	};
	eventStream: {
		topicsText: string;
	};
};
