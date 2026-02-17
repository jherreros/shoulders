import { SectionBox } from '@kinvolk/headlamp-plugin/lib/CommonComponents';
import {
	Alert,
	Box,
	Button,
	Card,
	CardContent,
	Checkbox,
	Chip,
	Divider,
	Dialog,
	DialogActions,
	DialogContent,
	DialogTitle,
	FormControl,
	Grid,
	InputLabel,
	ListItemText,
	MenuItem,
	Select,
	Stack,
	Tab,
	Tabs,
	TextField,
	Typography,
} from '@mui/material';
import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { fetchResourceList } from '../api';
import { formatTimestamp } from '../portalUtils';
import { resourceConfigs } from '../resourceConfigs';
import { ResourceItem } from '../types';
import { CreateResourceDialog } from './CreateResourceDialog';

function getStatusLabel(value: boolean | null) {
	if (value === true) return 'Healthy';
	if (value === false) return 'Unhealthy';
	return 'Unknown';
}

function getSyncLabel(value: boolean | null) {
	if (value === true) return 'In Sync';
	if (value === false) return 'Out of Sync';
	return 'Sync Unknown';
}

function getReadyLabel(value: boolean | null) {
	if (value === true) return 'Ready';
	if (value === false) return 'Not Ready';
	return 'Readiness Unknown';
}

function getDisplaySpec(rawSpec: Record<string, any> | undefined) {
	if (!rawSpec || typeof rawSpec !== 'object') return {};
	const spec = { ...rawSpec };
	delete spec.crossplane;
	return spec;
}

function getStatusColor(value: boolean | null): 'success' | 'error' | 'default' {
	if (value === true) return 'success';
	if (value === false) return 'error';
	return 'default';
}

function matchesWorkspaceFilter(item: ResourceItem, resourceId: string, selectedWorkspaces: string[]) {
	if (selectedWorkspaces.length === 0) return true;
	if (resourceId === 'workspaces') {
		return selectedWorkspaces.includes(item.name);
	}
	return selectedWorkspaces.includes(item.namespace);
}

export function ShouldersPortal() {
	const [activeTab, setActiveTab] = useState(0);
	const [resources, setResources] = useState<Record<string, ResourceItem[]>>({});
	const [loading, setLoading] = useState(false);
	const [warning, setWarning] = useState('');
	const [search, setSearch] = useState('');
	const [selectedWorkspaces, setSelectedWorkspaces] = useState<string[]>([]);
	const [createOpen, setCreateOpen] = useState(false);
	const [selectedResource, setSelectedResource] = useState<ResourceItem | null>(null);

	const refresh = useCallback(async () => {
		setLoading(true);
		setWarning('');
		try {
			const results = await Promise.all(
				resourceConfigs.map(async (config) => ({ config, ...(await fetchResourceList(config)) }))
			);
			setResources(Object.fromEntries(results.map((result) => [result.config.id, result.items])));
			const errors = results.filter((result) => result.error);
			if (errors.length > 0) {
				const notFound = errors.filter((result) => /not found/i.test(result.error));
				const otherErrors = errors.filter((result) => !/not found/i.test(result.error));
				const messages: string[] = [];
				if (notFound.length > 0) {
					messages.push(`Missing APIs: ${notFound.map((result) => result.config.label).join(', ')}`);
				}
				if (otherErrors.length > 0) {
					messages.push(`Unable to reach the Kubernetes API: ${otherErrors[0].error}`);
				}
				setWarning(messages.join('. '));
			}
		} finally {
			setLoading(false);
		}
	}, []);

	useEffect(() => {
		refresh();
	}, [refresh]);

	const activeConfig = resourceConfigs[activeTab];
	const activeResources = resources[activeConfig.id] ?? [];
	const workspaceOptions = useMemo(
		() =>
			(resources.workspaces ?? [])
				.map((item) => item.name)
				.filter(Boolean)
				.sort((a, b) => a.localeCompare(b)),
		[resources.workspaces]
	);
	const workspaceScopedResources = useMemo(
		() =>
			activeResources.filter((item) =>
				matchesWorkspaceFilter(item, activeConfig.id, selectedWorkspaces)
			),
		[activeConfig.id, activeResources, selectedWorkspaces]
	);

	const snapshotCounts = useMemo(
		() =>
			Object.fromEntries(
				resourceConfigs.map((config) => [
					config.id,
					(resources[config.id] ?? []).filter((item) =>
						matchesWorkspaceFilter(item, config.id, selectedWorkspaces)
					).length,
				])
			),
		[resources, selectedWorkspaces]
	);

	const filteredResources = useMemo(() => {
		const searchValue = search.trim().toLowerCase();
		return workspaceScopedResources.filter((item) => {
			const matchesSearch =
				!searchValue ||
				item.name.toLowerCase().includes(searchValue) ||
				item.namespace.toLowerCase().includes(searchValue);
			return matchesSearch;
		});
	}, [search, workspaceScopedResources]);

	const selectedConditions = useMemo(() => {
		if (!selectedResource) return [];
		const conditions = selectedResource.raw?.status?.conditions;
		if (!Array.isArray(conditions)) return [];
		return conditions.filter((condition: any) =>
			['Synced', 'Ready'].includes(condition?.type)
		);
	}, [selectedResource]);

	const selectedHealthSummary = useMemo(() => {
		if (!selectedResource) return '';
		if (selectedResource.synced === true && selectedResource.ready === true) {
			return 'This resource is healthy and operating normally.';
		}
		if (selectedResource.synced === false || selectedResource.ready === false) {
			return 'This resource needs attention. Review the condition details below.';
		}
		return 'Health status is still being reported by the platform.';
	}, [selectedResource]);

	const selectedDisplaySpec = useMemo(
		() => getDisplaySpec(selectedResource?.raw?.spec),
		[selectedResource]
	);

	return (
		<Box sx={{ padding: 3 }}>
			<Stack spacing={2}>
				<Box display="flex" justifyContent="space-between" gap={2} flexWrap="wrap">
					<Box>
						<Typography variant="h4" gutterBottom>
							Shoulders Portal
						</Typography>
						<Typography color="text.secondary">
							Track Crossplane-powered building blocks and provisioned resources across the cluster.
						</Typography>
					</Box>
					<FormControl size="small" sx={{ minWidth: 280 }}>
						<InputLabel id="workspace-filter-label">Workspace</InputLabel>
						<Select
							labelId="workspace-filter-label"
							multiple
							value={selectedWorkspaces}
							label="Workspace"
							onChange={(event) =>
								setSelectedWorkspaces(event.target.value as string[])
							}
							renderValue={(selected) => (selected as string[]).join(', ')}
						>
							{workspaceOptions.map((workspace) => (
								<MenuItem key={workspace} value={workspace}>
									<Checkbox checked={selectedWorkspaces.includes(workspace)} />
									<ListItemText primary={workspace} />
								</MenuItem>
							))}
						</Select>
					</FormControl>
				</Box>

				{warning && <Alert severity="warning">{warning}</Alert>}

				<SectionBox title="Platform Snapshot">
					<Grid container spacing={2}>
						{resourceConfigs.map((config, index) => (
							<Grid item xs={12} md={6} lg={3} key={config.id}>
								<Card
									variant="outlined"
									onClick={() => setActiveTab(index)}
									sx={{ cursor: 'pointer' }}
								>
									<CardContent>
										<Stack spacing={1}>
											<Typography variant="subtitle2" color="text.secondary">
												{config.label}
											</Typography>
											<Typography variant="h4">
												{snapshotCounts[config.id] ?? 0}
											</Typography>
											<Typography variant="body2" color="text.secondary">
												{config.description}
											</Typography>
										</Stack>
									</CardContent>
								</Card>
							</Grid>
						))}
					</Grid>
				</SectionBox>

				<Divider />

				<Stack spacing={2}>
					<Box display="flex" alignItems="center" justifyContent="space-between" flexWrap="wrap" gap={2}>
						<Typography variant="h5">Resources</Typography>
						<Stack direction="row" spacing={1}>
							<Button variant="outlined" onClick={() => setCreateOpen(true)}>
								Create
							</Button>
							<Button variant="contained" onClick={refresh} disabled={loading}>
								{loading ? 'Refreshing...' : 'Refresh'}
							</Button>
						</Stack>
					</Box>
					<Tabs
						value={activeTab}
						onChange={(_event, value) => setActiveTab(value)}
						textColor="primary"
						indicatorColor="primary"
					>
						{resourceConfigs.map((config) => (
							<Tab key={config.id} label={config.label} />
						))}
					</Tabs>
					<Stack direction={{ xs: 'column', md: 'row' }} spacing={2}>
						<TextField
							label="Search"
							value={search}
							onChange={(event) => setSearch(event.target.value)}
							size="small"
						/>
						<Chip
							label={`${filteredResources.length} of ${workspaceScopedResources.length}`}
							variant="outlined"
						/>
					</Stack>
					<Card variant="outlined">
						<CardContent>
							{filteredResources.length === 0 ? (
								<Typography color="text.secondary">No resources match the current filters.</Typography>
							) : (
								<Stack spacing={1}>
									{filteredResources.map((item) => (
										<Box
											key={`${item.namespace}-${item.name}`}
											display="flex"
											justifyContent="space-between"
											alignItems="center"
											flexWrap="wrap"
											gap={2}
											onClick={() => setSelectedResource(item)}
											sx={{ cursor: 'pointer' }}
										>
											<Box>
												<Typography variant="subtitle1">{item.name}</Typography>
												{activeConfig.namespaced && (
													<Typography variant="body2" color="text.secondary">
														Namespace: {item.namespace || 'default'}
													</Typography>
												)}
												<Typography variant="body2" color="text.secondary">
													Created: {formatTimestamp(item.createdAt)}
												</Typography>
											</Box>
											<Stack direction="row" spacing={1}>
												<Chip label={activeConfig.label} variant="outlined" />
												<Chip
													label={getSyncLabel(item.synced)}
													color={getStatusColor(item.synced)}
													variant="filled"
												/>
												<Chip
													label={getReadyLabel(item.ready)}
													color={getStatusColor(item.ready)}
													variant="filled"
												/>
											</Stack>
										</Box>
									))}
								</Stack>
							)}
						</CardContent>
					</Card>
				</Stack>
			</Stack>
			<CreateResourceDialog
				open={createOpen}
				onClose={() => setCreateOpen(false)}
				workspaceFilter={selectedWorkspaces[0] ?? ''}
				activeConfig={activeConfig}
				onCreated={refresh}
			/>
			<Dialog
				open={Boolean(selectedResource)}
				onClose={() => setSelectedResource(null)}
				maxWidth="md"
				fullWidth
			>
				<DialogTitle>Resource Details</DialogTitle>
				<DialogContent>
					{selectedResource && (
						<Stack spacing={2} marginTop={1}>
							<Typography variant="subtitle1">{selectedResource.name}</Typography>
							<Typography color="text.secondary">
								Namespace: {selectedResource.namespace || 'cluster-scoped'}
							</Typography>
							<Typography color="text.secondary">
								Created: {formatTimestamp(selectedResource.createdAt)}
							</Typography>
							<Stack direction="row" spacing={1}>
								<Chip
									label={getSyncLabel(selectedResource.synced)}
									color={getStatusColor(selectedResource.synced)}
									variant="filled"
								/>
								<Chip
									label={getReadyLabel(selectedResource.ready)}
									color={getStatusColor(selectedResource.ready)}
									variant="filled"
								/>
							</Stack>
							<Alert severity={selectedResource.synced && selectedResource.ready ? 'success' : 'warning'}>
								{selectedHealthSummary}
							</Alert>
							{selectedConditions.length > 0 && (
								<Box>
									<Typography variant="subtitle2" gutterBottom>
										Checks
									</Typography>
									<Stack spacing={1}>
										{selectedConditions.map((condition: any) => (
											<Box key={`${condition?.type}-${condition?.lastTransitionTime ?? ''}`}>
												<Typography variant="body2">
													{condition?.type}: {condition?.status} {condition?.reason ? `(${condition.reason})` : ''}
												</Typography>
												{condition?.message && (
													<Typography variant="body2" color="text.secondary">
														{condition.message}
													</Typography>
												)}
											</Box>
										))}
									</Stack>
								</Box>
							)}
							<Box>
								<Typography variant="subtitle2" gutterBottom>
									Spec
								</Typography>
								<Box component="pre" sx={{ margin: 0, overflow: 'auto' }}>
									{JSON.stringify(selectedDisplaySpec, null, 2)}
								</Box>
							</Box>
							<Box>
								<Typography variant="subtitle2" gutterBottom>
									Status
								</Typography>
								<Box component="pre" sx={{ margin: 0, overflow: 'auto' }}>
									{JSON.stringify(selectedResource.raw?.status ?? {}, null, 2)}
								</Box>
							</Box>
						</Stack>
					)}
				</DialogContent>
				<DialogActions>
					<Button onClick={() => setSelectedResource(null)}>Close</Button>
				</DialogActions>
			</Dialog>
		</Box>
	);
}
