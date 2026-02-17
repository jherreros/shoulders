# Artifact Hub metadata for Headlamp plugins

This directory is the source path to register in Artifact Hub as a **Headlamp plugins repository**.

Recommended repository URL when adding it in Artifact Hub:

- `https://github.com/juan/shoulders/artifacthub/headlamp-plugins`

On each git tag release (`v*`), CI generates and commits:

- `artifacthub/headlamp-plugins/shoulders-portal/<version>/artifacthub-pkg.yml`
- `artifacthub/headlamp-plugins/shoulders-portal/<version>/README.md`

The generated package metadata points to the matching GitHub Release tarball and checksum.

## One-time setup in Artifact Hub

1. Sign in to Artifact Hub.
2. Add a new repository of kind **Headlamp plugins**.
3. Set the repository URL to this directory path.
4. Ensure the branch is `main`.
5. After indexing, use the package URL from Artifact Hub in Headlamp `pluginsManager.configContent`.
