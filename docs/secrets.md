# Secrets

User space is delivered by Flux from git, so a tenant secret has to be safe in
git. Shoulders uses **SOPS with age**: the file is encrypted in the repository
and decrypted by Flux in the cluster.

## The flow

```sh
shoulders secrets init          # once per machine
shoulders up                    # publishes the key to every cluster
sops -e -i 3-user-space/team-a/app-config.sops.yaml
git add 3-user-space/team-a/app-config.sops.yaml .sops.yaml
```

`secrets init`:

1. generates an age identity **outside the repository**, in the sops CLI's own
   key file (`$XDG_CONFIG_HOME/sops/age/keys.txt`, or the platform's config
   directory — on macOS `~/Library/Application Support/sops/age/keys.txt`), so
   `sops -e`/`sops -d` work with no environment variables;
2. writes a `.sops.yaml` creation rule in the repository with the **public**
   recipient, so `sops -e` targets the right key without `--age`.

`shoulders up` reads that identity and creates `flux-system/sops-age` in every
cluster, then renders a `decryption` block on every Flux Kustomization:

```yaml
spec:
  decryption:
    provider: sops
    secretRef:
      name: sops-age
```

Both tiers get it: the hub's manifests and a spoke's are delivered the same
way, and a tenant secret belongs to the spoke that runs the tenant.

## What is and is not protected

- **In git**: only ciphertext, plus `.sops.yaml`, which contains a public key.
  Commit it when the team shares one key; with per-machine keys, leave it out
  of git — a committed recipient would silently make everyone encrypt to that
  one machine.
- **On the machine**: the private key, mode `0600`, outside the checkout.
- **In the cluster**: the private key as a Secret in `flux-system`. Read access
  to it is equivalent to read access to every tenant secret in that cluster, so
  it belongs to the same trust domain as cluster-admin. There is no per-cluster
  or per-tenant key yet; on a real installation, per-spoke keys are the natural
  next step.
- **Rotation** is manual: `shoulders secrets init --force`, re-encrypt
  (`sops updatekeys` or `sops -e` again), then `shoulders up`.

## Why not a secret store

A store (Vault, External Secrets, the cloud providers' own) is a second
distributed system to run, back up and bootstrap — and it has to be reachable
from every spoke, which is exactly the cross-cluster problem the topology work
spends its effort avoiding. SOPS moves no secret over the network: the file is
in the artifact flux already pulls, and the key is in the cluster flux already
runs. It is the smallest thing that is actually secure in git.

A store wins when secrets must rotate without a commit, be shared by many
services, or be audited per access. None of those is a laptop target.

## The sample

`3-user-space/team-a/webapp.yaml` reads `team-a-app-config` with
`optional: true`, so a fresh install — which has no key — starts without it. An
encrypted `app-config.sops.yaml` in the same kustomization turns it into a real
secret without changing the workload:

```yaml
# app-config.sops.yaml
apiVersion: v1
kind: Secret
metadata:
  name: team-a-app-config
  namespace: team-a
type: Opaque
stringData:
  APP_GREETING: hello-from-sops
```

If you commit an encrypted file, commit it encrypted to **your** key and make
sure the `optional: true` stays: anyone cloning the repository without that key
would otherwise get a Kustomization that never becomes Ready.
