import { dump } from 'js-yaml';
import { CreateFormState, ResourceConfig, ResourceItem } from './types';

function getConditionStatus(item: any, type: string): boolean | null {
	const conditions = item?.status?.conditions;
	if (!Array.isArray(conditions)) return null;
	const condition = conditions.find((entry: any) => entry?.type === type);
	if (!condition) return null;
	if (condition.status === 'True') return true;
	if (condition.status === 'False') return false;
	return null;
}

export function mapItems(items: any[]): ResourceItem[] {
	return items.map((item) => ({
		name: item?.metadata?.name ?? 'unknown',
		namespace: item?.metadata?.namespace ?? '',
		createdAt: item?.metadata?.creationTimestamp ?? '',
		synced: getConditionStatus(item, 'Synced'),
		ready: getConditionStatus(item, 'Ready'),
		raw: item ?? {},
	}));
}

export function formatTimestamp(value: string) {
	if (!value) return '-';
	const date = new Date(value);
	if (Number.isNaN(date.getTime())) return value;
	return date.toLocaleString();
}

export function getDefaultCreateState(namespaceFilter: string): CreateFormState {
	return {
		name: '',
		namespace: namespaceFilter,
		webapp: {
			image: 'nginx',
			tag: 'latest',
			replicas: '1',
			host: '',
		},
		stateStore: {
			postgresEnabled: true,
			postgresStorage: '1Gi',
			postgresDatabases: '',
			redisEnabled: true,
			redisReplicas: '1',
		},
		eventStream: {
			topicsText: '',
		},
	};
}

export function getCreatePath(config: ResourceConfig, namespace: string) {
	if (config.namespaced) {
		return `/apis/shoulders.io/v1alpha1/namespaces/${namespace}/${config.plural}`;
	}
	return `/apis/shoulders.io/v1alpha1/${config.plural}`;
}

export function parseListInput(value: string) {
	return value
		.split(/[\n,]+/)
		.map((entry) => entry.trim())
		.filter(Boolean);
}

export function buildManifest(config: ResourceConfig, form: CreateFormState) {
	const metadata: { name: string; namespace?: string } = { name: form.name.trim() };
	if (config.namespaced) {
		metadata.namespace = form.namespace.trim();
	}

	if (config.id === 'workspaces') {
		return { apiVersion: config.apiVersion, kind: config.kind, metadata, spec: {} };
	}

	if (config.id === 'webapplications') {
		const replicas = Number(form.webapp.replicas);
		return {
			apiVersion: config.apiVersion,
			kind: config.kind,
			metadata,
			spec: {
				image: form.webapp.image.trim(),
				tag: form.webapp.tag.trim(),
				replicas: Number.isFinite(replicas) ? replicas : 1,
				host: form.webapp.host.trim(),
			},
		};
	}

	if (config.id === 'statestores') {
		const databases = parseListInput(form.stateStore.postgresDatabases);
		const redisReplicas = Number(form.stateStore.redisReplicas);
		return {
			apiVersion: config.apiVersion,
			kind: config.kind,
			metadata,
			spec: {
				postgresql: {
					enabled: form.stateStore.postgresEnabled,
					storage: form.stateStore.postgresStorage.trim() || '1Gi',
					databases,
				},
				redis: {
					enabled: form.stateStore.redisEnabled,
					replicas: Number.isFinite(redisReplicas) ? redisReplicas : 1,
				},
			},
		};
	}

	const topics = parseListInput(form.eventStream.topicsText).map((name) => ({ name }));
	return {
		apiVersion: config.apiVersion,
		kind: config.kind,
		metadata,
		spec: topics.length > 0 ? { topics } : {},
	};
}

export function validateForm(config: ResourceConfig, form: CreateFormState) {
	if (!form.name.trim()) {
		return 'Name is required.';
	}
	if (config.namespaced && !form.namespace.trim()) {
		return 'Namespace is required for namespaced resources.';
	}
	if (config.id === 'webapplications') {
		if (!form.webapp.image.trim() || !form.webapp.tag.trim() || !form.webapp.host.trim()) {
			return 'Image, tag, and host are required.';
		}
		const replicas = Number(form.webapp.replicas);
		if (!Number.isFinite(replicas) || replicas < 1) {
			return 'Replicas must be a positive number.';
		}
	}
	if (config.id === 'statestores') {
		if (!form.stateStore.postgresEnabled && !form.stateStore.redisEnabled) {
			return 'Enable PostgreSQL or Redis (or both).';
		}
	}
	return '';
}

export function manifestToYaml(manifest: object) {
	return dump(manifest, { noRefs: true, lineWidth: 120 });
}
