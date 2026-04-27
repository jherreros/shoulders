import {
	buildManifest,
	getCreatePath,
	getDefaultCreateState,
	parseListInput,
	platformUIURL,
	validateForm,
} from './portalUtils';
import { resourceConfigs } from './resourceConfigs';

describe('portalUtils', () => {
	it('parses comma and newline list input', () => {
		expect(parseListInput('a,b\n c')).toEqual(['a', 'b', 'c']);
	});

	it('builds namespaced webapplication manifest', () => {
		const config = resourceConfigs.find((item) => item.id === 'webapplications');
		expect(config).toBeDefined();
		const form = getDefaultCreateState('team-a');
		form.name = 'demo';
		form.webapp.image = 'nginx';
		form.webapp.tag = '1.27';
		form.webapp.replicas = '3';
		form.webapp.host = 'demo.local';

		const manifest = buildManifest(config!, form) as Record<string, any>;

		expect(manifest.metadata).toEqual({ name: 'demo', namespace: 'team-a' });
		expect(manifest.spec).toMatchObject({
			image: 'nginx',
			tag: '1.27',
			replicas: 3,
			host: 'demo.local',
		});
	});

	it('validates required webapplication fields', () => {
		const config = resourceConfigs.find((item) => item.id === 'webapplications');
		expect(config).toBeDefined();
		const form = getDefaultCreateState('team-a');
		form.name = 'demo';
		form.webapp.image = '';

		expect(validateForm(config!, form)).toBe('Image, tag, and host are required.');
	});

	it('builds namespaced create path', () => {
		const config = resourceConfigs.find((item) => item.id === 'eventstreams');
		expect(config).toBeDefined();
		expect(getCreatePath(config!, 'team-a')).toBe(
			'/apis/shoulders.io/v1alpha1/namespaces/team-a/eventstreams'
		);
	});

	it('builds statestore object storage buckets', () => {
		const config = resourceConfigs.find((item) => item.id === 'statestores');
		expect(config).toBeDefined();
		const form = getDefaultCreateState('team-a');
		form.name = 'team-a-assets';
		form.stateStore.postgresEnabled = false;
		form.stateStore.redisEnabled = false;
		form.stateStore.objectStorageEnabled = true;
		form.stateStore.objectBuckets = 'team-a-assets, team-a-backups';
		form.stateStore.objectOwner = true;

		const manifest = buildManifest(config!, form) as Record<string, any>;

		expect(manifest.spec.objectStorage).toEqual({
			enabled: true,
			buckets: [
				{ name: 'team-a-assets', read: true, write: true, owner: true },
				{ name: 'team-a-backups', read: true, write: true, owner: true },
			],
		});
	});

	it('requires buckets when object storage is enabled', () => {
		const config = resourceConfigs.find((item) => item.id === 'statestores');
		expect(config).toBeDefined();
		const form = getDefaultCreateState('team-a');
		form.name = 'team-a-assets';
		form.stateStore.postgresEnabled = false;
		form.stateStore.redisEnabled = false;
		form.stateStore.objectStorageEnabled = true;

		expect(validateForm(config!, form)).toBe('Add at least one object storage bucket.');
	});

	it('derives sibling platform hosts from the current headlamp domain', () => {
		expect(platformUIURL('grafana', 'headlamp.lvh.me')).toBe('http://grafana.lvh.me');
		expect(platformUIURL('dex', 'headlamp.lvh.me')).toBe('https://dex.lvh.me');
	});

	it('falls back to default platform hosts outside headlamp', () => {
		expect(platformUIURL('reporter')).toBe('http://reporter.localhost');
	});
});
