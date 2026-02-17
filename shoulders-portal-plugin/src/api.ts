import { ApiProxy } from '@kinvolk/headlamp-plugin/lib';
import { mapItems } from './portalUtils';
import { ResourceConfig } from './types';

export async function fetchResourceList(config: ResourceConfig) {
	try {
		const response = await ApiProxy.request(config.path);
		return { items: mapItems(response?.items ?? []), error: '' };
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		return { items: [], error: message };
	}
}
