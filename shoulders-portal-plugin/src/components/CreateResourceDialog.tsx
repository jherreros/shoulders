import { ApiProxy } from '@kinvolk/headlamp-plugin/lib';
import {
	Alert,
	Button,
	Dialog,
	DialogActions,
	DialogContent,
	DialogTitle,
	FormControl,
	FormControlLabel,
	InputLabel,
	MenuItem,
	Select,
	Stack,
	Switch,
	Tab,
	Tabs,
	TextField,
} from '@mui/material';
import { load } from 'js-yaml';
import React, { useEffect, useState } from 'react';
import { resourceConfigs } from '../resourceConfigs';
import {
	buildManifest,
	getCreatePath,
	getDefaultCreateState,
	manifestToYaml,
	validateForm,
} from '../portalUtils';
import { CreateFormState, CreateMode, ResourceConfig } from '../types';

type CreateResourceDialogProps = {
	open: boolean;
	onClose: () => void;
	workspaceFilter: string;
	activeConfig: ResourceConfig;
	onCreated: () => Promise<void>;
};

export function CreateResourceDialog({
	open,
	onClose,
	workspaceFilter,
	activeConfig,
	onCreated,
}: CreateResourceDialogProps) {
	const [createMode, setCreateMode] = useState<CreateMode>('form');
	const [createResourceId, setCreateResourceId] = useState(resourceConfigs[0]?.id ?? '');
	const [createForm, setCreateForm] = useState<CreateFormState>(() =>
		getDefaultCreateState(workspaceFilter)
	);
	const [createYaml, setCreateYaml] = useState('');
	const [createError, setCreateError] = useState('');
	const [creating, setCreating] = useState(false);

	const createConfig =
		resourceConfigs.find((config) => config.id === createResourceId) ?? activeConfig;

	useEffect(() => {
		if (!open) return;
		const defaults = getDefaultCreateState(workspaceFilter);
		setCreateResourceId(activeConfig.id);
		setCreateForm(defaults);
		setCreateMode('form');
		setCreateYaml(manifestToYaml(buildManifest(activeConfig, defaults)));
		setCreateError('');
		setCreating(false);
	}, [activeConfig, open, workspaceFilter]);

	useEffect(() => {
		if (!open || createMode !== 'form') return;
		setCreateYaml(manifestToYaml(buildManifest(createConfig, createForm)));
	}, [createConfig, createForm, createMode, open]);

	const closeDialog = () => {
		setCreateError('');
		setCreating(false);
		onClose();
	};

	const handleCreateResourceChange = (value: string) => {
		const config = resourceConfigs.find((item) => item.id === value) ?? activeConfig;
		const defaults = getDefaultCreateState(workspaceFilter);
		setCreateResourceId(value);
		setCreateForm(defaults);
		setCreateYaml(manifestToYaml(buildManifest(config, defaults)));
		setCreateError('');
	};

	const handleCreateModeChange = (_event: React.SyntheticEvent, value: CreateMode) => {
		if (!value) return;
		if (value === 'form') {
			try {
				const parsed = load(createYaml);
				if (parsed && typeof parsed === 'object') {
					const config = createConfig;
					const manifest = parsed as Record<string, any>;
					setCreateForm((prev) => {
						const name = manifest?.metadata?.name ?? prev.name;
						const namespace = manifest?.metadata?.namespace ?? prev.namespace;
						if (config.id === 'webapplications') {
							return {
								...prev,
								name,
								namespace,
								webapp: {
									image: manifest?.spec?.image ?? prev.webapp.image,
									tag: manifest?.spec?.tag ?? prev.webapp.tag,
									replicas: String(manifest?.spec?.replicas ?? prev.webapp.replicas),
									host: manifest?.spec?.host ?? prev.webapp.host,
								},
							};
						}
						if (config.id === 'statestores') {
							return {
								...prev,
								name,
								namespace,
								stateStore: {
									postgresEnabled:
										manifest?.spec?.postgresql?.enabled ?? prev.stateStore.postgresEnabled,
									postgresStorage:
										manifest?.spec?.postgresql?.storage ?? prev.stateStore.postgresStorage,
									postgresDatabases: (manifest?.spec?.postgresql?.databases ?? []).join(', '),
									redisEnabled:
										manifest?.spec?.redis?.enabled ?? prev.stateStore.redisEnabled,
									redisReplicas: String(
										manifest?.spec?.redis?.replicas ?? prev.stateStore.redisReplicas
									),
								},
							};
						}
						if (config.id === 'eventstreams') {
							return {
								...prev,
								name,
								namespace,
								eventStream: {
									topicsText: (manifest?.spec?.topics ?? [])
										.map((topic: { name?: string }) => topic?.name)
										.filter(Boolean)
										.join('\n'),
								},
							};
						}
						return { ...prev, name, namespace };
					});
				}
			} catch (error) {
				const message = error instanceof Error ? error.message : String(error);
				setCreateError(`Unable to parse YAML: ${message}`);
			}
		}
		setCreateMode(value);
	};

	const handleCreateSubmit = async () => {
		setCreateError('');
		const selectedConfig = createConfig;
		let manifest: Record<string, any>;
		let config = selectedConfig;
		if (createMode === 'form') {
			const error = validateForm(config, createForm);
			if (error) {
				setCreateError(error);
				return;
			}
			manifest = buildManifest(config, createForm) as Record<string, any>;
		} else {
			try {
				const parsed = load(createYaml);
				if (!parsed || typeof parsed !== 'object') {
					setCreateError('YAML must describe a Kubernetes object.');
					return;
				}
				manifest = parsed as Record<string, any>;
				const manifestKind = manifest?.kind;
				const manifestApi = manifest?.apiVersion;
				if (!manifestKind || !manifestApi) {
					setCreateError('YAML must include apiVersion and kind.');
					return;
				}
				const matchedConfig =
					resourceConfigs.find(
						(item) => item.kind === manifestKind && item.apiVersion === manifestApi
					) ?? resourceConfigs.find((item) => item.kind === manifestKind);
				if (!matchedConfig) {
					setCreateError(`Unsupported kind: ${manifestKind}.`);
					return;
				}
				config = matchedConfig;
			} catch (error) {
				const message = error instanceof Error ? error.message : String(error);
				setCreateError(`Unable to parse YAML: ${message}`);
				return;
			}
		}

		if (!manifest?.metadata?.name) {
			setCreateError('metadata.name is required.');
			return;
		}

		if (config.namespaced) {
			const namespace = manifest?.metadata?.namespace || createForm.namespace.trim();
			if (!namespace) {
				setCreateError('metadata.namespace is required for namespaced resources.');
				return;
			}
			manifest.metadata = { ...manifest.metadata, namespace };
		}

		const namespace = config.namespaced ? manifest.metadata.namespace : '';
		const createPath = getCreatePath(config, namespace);

		setCreating(true);
		try {
			await ApiProxy.post(createPath, manifest);
			closeDialog();
			await onCreated();
		} catch (error) {
			const message = error instanceof Error ? error.message : String(error);
			setCreateError(`Unable to create resource: ${message}`);
			setCreating(false);
		}
	};

	return (
		<Dialog open={open} onClose={closeDialog} maxWidth="md" fullWidth>
			<DialogTitle>Create Resource</DialogTitle>
			<DialogContent>
				<Stack spacing={2} marginTop={1}>
					{createError && <Alert severity="error">{createError}</Alert>}
					<FormControl fullWidth size="small">
						<InputLabel id="create-resource-type">Resource</InputLabel>
						<Select
							labelId="create-resource-type"
							label="Resource"
							value={createResourceId}
							onChange={(event) => handleCreateResourceChange(event.target.value)}
						>
							{resourceConfigs.map((config) => (
								<MenuItem key={config.id} value={config.id}>
									{config.label}
								</MenuItem>
							))}
						</Select>
					</FormControl>
					<Tabs
						value={createMode}
						onChange={handleCreateModeChange}
						textColor="primary"
						indicatorColor="primary"
					>
						<Tab label="Form" value="form" />
						<Tab label="YAML" value="yaml" />
					</Tabs>
					{createMode === 'form' ? (
						<Stack spacing={2}>
							<TextField
								label="Name"
								value={createForm.name}
								onChange={(event) =>
									setCreateForm((prev) => ({ ...prev, name: event.target.value }))
								}
								size="small"
								required
							/>
							{createConfig.namespaced && (
								<TextField
									label="Namespace"
									value={createForm.namespace}
									onChange={(event) =>
										setCreateForm((prev) => ({
											...prev,
											namespace: event.target.value,
										}))
									}
									size="small"
									required
								/>
							)}
							{createConfig.id === 'webapplications' && (
								<Stack spacing={2}>
									<TextField
										label="Image"
										value={createForm.webapp.image}
										onChange={(event) =>
											setCreateForm((prev) => ({
												...prev,
												webapp: { ...prev.webapp, image: event.target.value },
											}))
										}
										size="small"
										required
									/>
									<TextField
										label="Tag"
										value={createForm.webapp.tag}
										onChange={(event) =>
											setCreateForm((prev) => ({
												...prev,
												webapp: { ...prev.webapp, tag: event.target.value },
											}))
										}
										size="small"
										required
									/>
									<TextField
										label="Replicas"
										value={createForm.webapp.replicas}
										onChange={(event) =>
											setCreateForm((prev) => ({
												...prev,
												webapp: { ...prev.webapp, replicas: event.target.value },
											}))
										}
										size="small"
										required
									/>
									<TextField
										label="Host"
										value={createForm.webapp.host}
										onChange={(event) =>
											setCreateForm((prev) => ({
												...prev,
												webapp: { ...prev.webapp, host: event.target.value },
											}))
										}
										size="small"
										required
									/>
								</Stack>
							)}
							{createConfig.id === 'statestores' && (
								<Stack spacing={2}>
									<FormControlLabel
										control={
											<Switch
												checked={createForm.stateStore.postgresEnabled}
												onChange={(event) =>
													setCreateForm((prev) => ({
														...prev,
														stateStore: {
															...prev.stateStore,
															postgresEnabled: event.target.checked,
														},
													}))
												}
												color="primary"
											/>
										}
										label="Enable PostgreSQL"
									/>
									<TextField
										label="PostgreSQL Storage"
										value={createForm.stateStore.postgresStorage}
										onChange={(event) =>
											setCreateForm((prev) => ({
												...prev,
												stateStore: {
													...prev.stateStore,
													postgresStorage: event.target.value,
												},
											}))
										}
										size="small"
									/>
									<TextField
										label="PostgreSQL Databases"
										value={createForm.stateStore.postgresDatabases}
										onChange={(event) =>
											setCreateForm((prev) => ({
												...prev,
												stateStore: {
													...prev.stateStore,
													postgresDatabases: event.target.value,
												},
											}))
										}
										size="small"
										placeholder="team-a-01, team-a-02"
										helperText="Comma or newline separated"
									/>
									<FormControlLabel
										control={
											<Switch
												checked={createForm.stateStore.redisEnabled}
												onChange={(event) =>
													setCreateForm((prev) => ({
														...prev,
														stateStore: {
															...prev.stateStore,
															redisEnabled: event.target.checked,
														},
													}))
												}
												color="primary"
											/>
										}
										label="Enable Redis"
									/>
									<TextField
										label="Redis Replicas"
										value={createForm.stateStore.redisReplicas}
										onChange={(event) =>
											setCreateForm((prev) => ({
												...prev,
												stateStore: {
													...prev.stateStore,
													redisReplicas: event.target.value,
												},
											}))
										}
										size="small"
									/>
								</Stack>
							)}
							{createConfig.id === 'eventstreams' && (
								<TextField
									label="Topics"
									value={createForm.eventStream.topicsText}
									onChange={(event) =>
										setCreateForm((prev) => ({
											...prev,
											eventStream: { topicsText: event.target.value },
										}))
									}
									placeholder="logs\nmetrics"
									helperText="One topic per line"
									minRows={4}
									multiline
									size="small"
								/>
							)}
						</Stack>
					) : (
						<TextField
							label="YAML"
							value={createYaml}
							onChange={(event) => setCreateYaml(event.target.value)}
							minRows={12}
							multiline
							size="small"
						/>
					)}
				</Stack>
			</DialogContent>
			<DialogActions>
				<Button onClick={closeDialog} disabled={creating}>
					Cancel
				</Button>
				<Button variant="contained" onClick={handleCreateSubmit} disabled={creating}>
					{creating ? 'Creating...' : 'Create'}
				</Button>
			</DialogActions>
		</Dialog>
	);
}
