# Local iteration with OCI snapshots

Test addon changes without pushing to GitHub:

```bash
shoulders up --local     # fresh cluster, Flux reconciles from your working tree
# ... edit 2-addons/... ...
shoulders sync           # push a new snapshot tag and wait for Flux to reconcile
shoulders sync --wait=false  # push and request reconcile without blocking
```

`sync` waits up to 10 minutes by default (`--timeout` overrides). On `vind` with no
`ociRepository.url` configured, both commands create a throwaway in-cluster registry
automatically. Every snapshot gets an immutable `local-<sha>-<timestamp>[-dirty]` tag.

Raw script path:

```bash
SHOULDERS_FLUX_SOURCE=oci SHOULDERS_OCI_URL=... SHOULDERS_OCI_TAG=... 2-addons/install-addons.sh
```
