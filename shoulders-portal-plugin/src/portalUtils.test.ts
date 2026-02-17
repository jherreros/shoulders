import { resourceConfigs } from './resourceConfigs';
import {
	buildManifest,
	getCreatePath,
	getDefaultCreateState,
	parseListInput,
	validateForm,
} from './portalUtils';

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
});
