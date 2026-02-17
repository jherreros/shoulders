# shoulders-portal-plugin

Shoulders portal plugin for Headlamp. It surfaces the Crossplane-backed resource catalog
for Workspaces, WebApplications, StateStores, and EventStreams.

## Local development

1. `npm install`
2. `npm run start`
3. Open Headlamp and navigate to the "Shoulders" entry under the Cluster menu.

## Packaging

1. `npm run build`
2. `npm run package`

The packaged plugin can be published to Artifact Hub and referenced in the Headlamp Helm
chart `pluginsManager` configuration.

## Release publishing

The repository release workflow now also packages this plugin and uploads these assets
to the GitHub Release that matches the pushed tag (for example `v0.1.0`):

- `shoulders-portal-plugin-<version>.tar.gz`
- `shoulders-portal-plugin-checksums.txt`

The workflow aligns the plugin `package.json` version to the release tag automatically
before packaging.

## Installing in-cluster

For Headlamp in-cluster `pluginsManager`, keep using an Artifact Hub package URL as
`source` in the plugin config.

GitHub Releases are now used as your build artifact distribution channel. If you want
fully automated in-cluster updates from your own plugin publication flow, publish each
plugin version to Artifact Hub and then update the version in the Headlamp Helm values.
