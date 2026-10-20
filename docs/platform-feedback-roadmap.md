# Platform Feedback Roadmap — Bank of Anthos + Astronomy Shop migrations

Date: 2026-09-26. Author: shoulders maintainers (synthesized from user feedback).
Sources:

- `bank-of-shoulders/docs/SHOULDERS-FEEDBACK.md` (2026-09-24, Bank of Anthos, `small` profile, workspace `bank`, Mac arm64) — all running, login + balances + payment verified at `http://bank.localhost`.
- `astro-shop/docs/SHOULDERS-FEEDBACK.md` (2026-09-24/25, OTel demo v3.1.0, `medium` profile, workspace `astro`, Mac arm64, 11 CPU / 11.67 GiB Docker Desktop) — full checkout E2E + Kafka E2E verified at `http://astro.localhost`, with flaky windows under pressure.

Constraint honored by both reporters: local `shoulders` repo was **not** read; everything came from `shoulders.juanherreros.com` docs + CLI `--help` + live behavior. This roadmap **does** read the local repo, so verdicts below correct the feedback where the code already supports something or where the ask is a wishlist.

How to read this document:

- §1 is the critical triage: every pain point, whether it holds up against the code, and what it costs.
- §2–§6 are the features, ordered CLI-robustness-first (explicit prioritization decision). Each feature contains tasks with file pointers, acceptance criteria, priority, and effort.
- §7 lists explicitly deferred / wishlist items so they stop resurfacing.
- Priorities: **P0** = fix now (correctness / data-loss / crash), **P1** = next (stability, parity, docs), **Future** = parked.
- Effort: **S** = hours, **M** = 1–3 days, **L** = up to a week / design decision.

---

## 1. Critical triage (evidence-checked)

### 1.1 Bank of Anthos feedback

| # | Pain | Verdict | Notes / evidence |
|---|---|---|---|
| B1 | `up` fails with raw `docker … Bind for 0.0.0.0:80 failed` | **Valid** | `shoulders-cli/cmd/up.go:EnsureVindCluster` has no port preflight. Fix in CLI. |
| B2 | Failed `up` leaves `Created` container blocking retries + wipes `~/.kube/config` to `clusters: null`, causing confusing `Install Cilium CNI: no configuration has been provided` | **Valid, high value** | No cleanup of half-created vind container, no kubeconfig backup. The Cilium error is a downstream symptom. |
| B3 | No arch guidance (vind nodes arm64, upstream amd64 pins → `exec format error`) | **Valid, docs-only, cheap** | Reporter calls this the longest detour. A short "images & architectures" note fixes it. |
| B4 | `ENABLE_TRACING=true` (GCP default) crashes apps without ADC, obscure grpc traceback | **Valid, docs-only** | Not a platform bug. One-line migration note ("disable cloud tracing locally") suffices. |
| B5a | `app init` has no probe timing tuning (Java wants 60–120s initial delay) | **Valid** | `shoulders-cli/cmd/app.go:buildHTTPProbe` hardcodes `initialDelaySeconds: 5, periodSeconds: 10`. XRD probes are `x-kubernetes-preserve-unknown-fields`, so the API accepts full probes — the CLI just can't set them. |
| B5b | No `valueFrom: fieldRef` (used static `NAMESPACE=bank`) | **Valid but low priority** | Niche; static workaround is fine. |
| B5c | `--secret-mount` mounts whole secrets only (had to remap key paths) | **Valid** | `buildVolumesAndMounts` parses only `secretName:mountPath[:volumeName]`; no `items:` mapping. XRD `volumes`/`volumeMounts` are generic, so this is CLI-only. |
| B5d | No `ephemeral-storage` requests/limits; no `fsGroup`/pod-level security context | **Half-valid** | `podSecurityContext` and generic `resources` already exist in `application-xrd.yaml` and are passed through by `application-composition.yaml`. Missing: CLI flags (`fsGroup`, ephemeral-storage examples/docs). Small CLI + docs gap, not an XRD gap. |
| B6 | `--init-sql` exists but no multi-statement/file example; seeded via `kubectl exec psql` instead | **Valid, docs-only** | Flag exists in `shoulders-cli/cmd/infra.go` (`StringArrayVar`). Needs file/multi-statement example under StateStore concepts. |
| B7 | Dev DB credential defaults (`username=app password=password`) visible in `statestore spec` but undocumented as dev-tier | **Valid, docs-only** | Defaults are in `state-store-xrd.yaml` (`username: app`, `password: password`). Either document as dev-tier or confirm intended. |
| B8 | Kyverno workspace-prefix rule forces renames (`ledgerwriter` → `bank-ledgerwriter`) + caller `*_API_ADDR` + Service port (`:8080` → `:80`) updates; not called out as migration consequence | **Valid, docs-only** | Prefix rule is documented; the migration blast radius isn't. |
| B9 | Jib-shaped Java apps (no Dockerfile, amd64 alpine Temurin pin) need plain Dockerfile + multi-arch base | **Valid, docs-only** | Tiny "migrating Jib apps" recipe covers it. |
| BX1 | `infra add-db/add-bucket/add-stream` have no `--dry-run` (had to reverse-engineer StateStore YAML from live object) | **Valid** | `app init` has `--dry-run`; infra cmds in `cmd/infra.go` apply directly. |
| BX2 | No documented GitOps end-state (CLI day-0 → committed manifests + `app apply -f` / Flux) | **Valid, docs-only** | Reporter converted a day late for lack of one paragraph. |
| BX3 | No `app init --from <deployment.yaml>` brownfield importer | **Wishlist / Future** | Large scope. Document the `kubectl get … -o yaml` + strip-server-fields escape hatch first. |

### 1.2 Astronomy Shop feedback

| # | Pain | Verdict | Notes / evidence |
|---|---|---|---|
| A1 | `up --set platform.profile=medium` boots medium but config file keeps `small`, so `infra add-stream` refuses until hand-edit | **Valid** | `cmd/root.go:loadRuntimeConfig` → `config.LoadWithOverrides` is in-memory only (only `up --local` persists OCI source via `saveCurrentConfig`). **Decision: warn-only + docs** (no persistence): print "overrides are ephemeral" and document editing `~/.shoulders/config.yaml`. |
| A2 | Medium on a laptop is heavy; no documented minimums per profile | **Valid, docs-only** | Profile footprints described qualitatively in README; no CPU/RAM/disk numbers. |
| A3 | Disk-pressure death spiral (~16 local images + pulls fill Docker disk → evictions → Kyverno dies → fail-closed webhooks freeze all pod mutations → Flux can't heal) | **Valid, biggest stability issue** | No disk preflight in `up` / `build-image` / `load-image`. Recovery required pruning, webhook `Ignore` flips, mass pod deletes, `stop`/`start`. |
| A4 | `load-image` target amnesiac across `stop`/`start` (ImagePullBackOff on previously-working pods; local copies pruned → forced rebuilds) | **Needs investigation** | Could be vind image-store semantics vs. user pruning. Task: reproduce first, then persist-or-warn. Do not assume the fix. |
| A5 | `app update --env` panics (`cannot deep copy []map[string]interface {}`) with full stack trace | **Valid P0 bug** | `cmd/app.go:applyAppFlagOverrides` assigns `parseEnvVars` result (`[]map[string]interface{}`) into `spec["env"]`; unstructured deep-copy requires `[]interface{}`. Workaround (regenerate `--dry-run` + `app apply -f`) proves the path. Fix + regression test. |
| A6a | No `--command/--args` on WebApplications (forced wrapper image for flagd) | **Half-inaccurate: CLI-only gap** | XRD (`application-xrd.yaml`) already has `command`/`args`; `application-composition.yaml` already templates them. `registerAppSpecFlags` in `cmd/app.go` never exposes them. Fix = wire flags, not an XRD change. |
| A6b | No file mounts (baked `otel-config.yml` into image) | **Valid CLI gap** | XRD volumes/mounts generic; CLI only offers `--secret-mount` + `--empty-dir`. Needs `--configmap-mount` / file-mount or generic volume flag. For comparison, `Workload` XRD already has `command`/`args` too. |
| A7 | Single `port`/`service.port` only (flagd's second OFREP 8016 port dropped) | **Valid but edge-case** | Composition hardcodes one `http` port + Service + HTTPRoute. Multi-port = XRD + composition + CLI change. Consumers tolerated the drop → deferrable. |
| A8 | gRPC/health story + `shoulders app doctor`-style required-env check (missed `PAYMENT_ADDR`, `OTEL_SERVICE_NAME`) | **Wishlist, discount** | Report says the ConfigMap omission was "caught fast via logs". Low ROI; `shoulders logs` already covers it. Parked. |
| A9 | OTel SDK vs `OTEL_SDK_DISABLED` behaves per-SDK (Go/Python/.NET honor; Ruby crashes; C++/PHP/Java-agent retry noisily → log volume → disk burn) | **Valid, docs-only** | Migration note ("disable per SDK, verify each") saves hours. Links to A3 (disk burn). |
| A10 | Upstream papercuts (Docker-Hub vs ghcr blocks, embedded `grpc-health-probe` stages) | **Not Shoulders** | At most one early-warning line in `build-image` docs for ghcr-blocked users. |
| A11 | zsh footgun (`$img:local` triggers `:l` modifier → `astro-emailocal:latest`) | **Valid, one-line docs fix** | Use `${img}:local` in `build-image`/`load-image` examples. |
| AX1 | Break-glass used: Kyverno webhooks Fail→Ignore, `flux suspend`, `trivy-operator scale 0`, stale `disk-pressure` taint clearing, CNPG finalizer removal, builder/image prune, `stop`/`start` cycles | **Valid ops gap** | Tribal checklist at handover ("keep `Ignore` until stable", "keep Trivy at 0 on laptop"). Needs a documented break-glass runbook + opt-out, not just handover notes. |
| AX2 | Postgres BestEffort instances (XRD offers no resource knob) die first on every disk-pressure wave; `-rw`/`-ro` endpoints empty until CNPG recovers | **Valid XRD gap** | `state-store-xrd.yaml` `postgresql` has `storage` only — no cpu/mem requests. Correlates with A3. Medium priority, after CLI robustness. |
| AX3 | Cold gRPC dials through gateway time out (retry succeeds); `shoulders.io` API resets under control-plane load while core API degrades gracefully | **Observe, not actionable yet** | No task proposed beyond documenting "retry once on cold dials" if it reproduces. Needs a second data point before API-server tuning work. |

---

## 2. Feature A — `up` preflights & failure hygiene (P0, do first)

**Pain addressed:** B1, B2, A1, A3-preflight-part, A2-headroom-part.

### Task A1 — Host port + conflict preflight in `up`
- **What:** before creating the vind cluster, check host TCP 80/443 are free. On conflict, print the squatting container/process name and suggest `shoulders down --name <X>` / `shoulders cluster list`.
- **Files:** `shoulders-cli/cmd/up.go`, `shoulders-cli/internal/bootstrap/` (vind create path), possibly `shoulders-cli/cmd/cluster.go` for listing.
- **Acceptance:** `up` with port 80 held by an old `shoulders-airgap` CP fails fast with a named remedy instead of a raw `docker … Bind … failed` mid-install.
- **Priority / effort:** P0 / S–M.

### Task A2 — Failure cleanup + kubeconfig backup
- **What:** on `up` failure, remove the half-created (`Created`-state) vind container so retries aren't blocked; snapshot `~/.kube/config` (or configured kubeconfig) before overwriting and restore/surface it on failure instead of leaving `clusters: null` → misleading `Install Cilium CNI: no configuration has been provided`.
- **Files:** `shoulders-cli/cmd/up.go`, `shoulders-cli/internal/bootstrap/`, `shoulders-cli/internal/kube/`.
- **Acceptance:** kill `up` mid-phase-1 twice in a row → second `up` starts clean; kubeconfig is never left nulled.
- **Priority / effort:** P0 / M.

### Task A3 — Disk (and CPU/mem) preflight for `up` / `build-image` / `load-image`
- **What:** check Docker disk headroom + CPU/mem before `up` (per-profile thresholds) and before `build-image`/`load-image` (image + cache size). Fail or warn with concrete remedy (`docker builder prune`, `docker image prune`, increase Docker Desktop disk beyond the 100 GB-class setup that still filled up).
- **Files:** `shoulders-cli/cmd/up.go`, `shoulders-cli/cmd/app.go` (`build-image`/`load-image`), `shoulders-cli/internal/bootstrap/`.
- **Acceptance:** `up --set platform.profile=medium` on a near-full Docker disk refuses with required-vs-available numbers instead of dying mid-reconcile into the A3 death spiral.
- **Priority / effort:** P0 / M. **Depends on:** A4-headroom numbers (Task D1) for thresholds.

### Task A4 — `--set` is ephemeral: warn + document (no persistence)
- **What (decision):** keep `--set key=value` ephemeral (in-memory override via `config.LoadWithOverrides` in `cmd/root.go`); do **not** persist to `~/.shoulders/config.yaml`. Instead: log an explicit notice on `up` (and in `--help`) that overrides are not saved, and document editing the config file for durable changes (e.g. `platform.profile: medium` for `infra add-stream` to accept EventStreams).
- **Files:** `shoulders-cli/cmd/root.go`, `shoulders-cli/cmd/up.go` (notice), CLI `--help` text, website getting-started/config docs.
- **Acceptance:** `up --set platform.profile=medium` prints "overrides are ephemeral, not saved to config file"; docs state the durable path.
- **Priority / effort:** P0-docs / S.

---

## 3. Feature B — CLI correctness & parity (P0)

**Pain addressed:** A5, A6a, A6b, BX1, B5a–d.

### Task B1 — Fix `app update --env` panic (P0 bug)
- **What:** `applyAppFlagOverrides` in `cmd/app.go` assigns `parseEnvVars` result (`[]map[string]interface{}`) into unstructured `spec["env"]`; unstructured deep-copy requires `[]interface{}`. Convert to `[]interface{}` (same for `envFrom`, `volumes`, `volumeMounts`, probes, resources paths).
- **Files:** `shoulders-cli/cmd/app.go`, `shoulders-cli/cmd/app_test.go` (regression test).
- **Acceptance:** `app update --env FOO=bar` on an existing WebApplication succeeds; unit test covers `--env` / `--env-from-*` / `--secret-mount` update paths without panic.
- **Priority / effort:** P0 / S.

### Task B2 — Expose `--command` / `--args` on `app init` / `app update`
- **What:** wire CLI flags through to the existing XRD fields. No XRD/composition change needed — `application-xrd.yaml` already defines `command`/`args` and `application-composition.yaml` already templates them. (Workload already has both fields; check `cmd/workload.go` exposes them and mirror the flags.)
- **Files:** `shoulders-cli/cmd/app.go` (`registerAppSpecFlags`, `buildWebApplication`, `applyAppFlagOverrides`), `shoulders-cli/pkg/api/v1alpha1/` types.
- **Acceptance:** `app init flagd --image … --command … --args … --dry-run -o yaml` renders `spec.command`/`spec.args`; `app update` can change them.
- **Priority / effort:** P0 / S.

### Task B3 — `--dry-run -o yaml` for `infra add-db` / `add-bucket` / `add-stream`
- **What:** mirror the `app init --dry-run` pattern: print the StateStore/EventStream YAML instead of applying. Document the reverse escape hatch (`kubectl get statestore|eventstream -o yaml`, strip `status`, `resourceVersion`, `uid`, `crossplane.resourceRefs`) so CLI-first users can convert to GitOps without reverse-engineering.
- **Files:** `shoulders-cli/cmd/infra.go`, website concepts/StateStore + EventStream pages.
- **Acceptance:** `infra add-db foo --dry-run -o yaml` prints committable YAML; `kubectl diff` after applying it is clean.
- **Priority / effort:** P0 / S–M.

### Task B4 — Probe timing, secret-items, fieldRef, ephemeral-storage, pod security flags
- **What (bundle of small CLI flags, each independently shippable):**
  - Probe tuning: `--probe-initial-delay`, `--probe-period` (or per-probe variants) instead of hardcoded `initialDelaySeconds: 5, periodSeconds: 10` in `buildHTTPProbe`. Needed for Java 60–120s startups (B5a).
  - `--secret-items` mapping (`secretName:mountPath:key1=path1,…`) so whole-secret mounts aren't forced (B5c).
  - `--env-from-fieldref` (e.g. `NAMESPACE` via `metadata.namespace`) instead of static values (B5b, low priority within the bundle).
  - `ephemeral-storage` requests/limits via `--ephemeral-storage-request/limit` (feeds generic `resources`, helps disk-pressure reasoning).
  - `--fs-group` / pod-level security context passthrough (XRD `podSecurityContext` already exists; CLI has no flag).
- **Files:** `shoulders-cli/cmd/app.go`, `shoulders-cli/cmd/helpers.go` (shared parsers), XRD docs tables in README/website (no XRD change required — fields are generic/preserve-unknown).
- **Acceptance:** each flag round-trips through `--dry-run -o yaml` into the expected `spec` field.
- **Priority / effort:** P0 (probes, secret-items) / P1 (rest); S each, M bundled.

### Task B5 — File / ConfigMap mounts on WebApplications
- **What:** add `--configmap-mount` (and/or generic file-mount) alongside existing `--secret-mount` / `--empty-dir`, so `otel-config.yml`-style files and flagd configs don't require wrapper/baked images. XRD `volumes`/`volumeMounts` are generic — CLI + docs work only.
- **Files:** `shoulders-cli/cmd/app.go` (`buildVolumesAndMounts`), website WebApplication guide.
- **Acceptance:** file from a ConfigMap mountable via one flag; documented example.
- **Priority / effort:** P0 / S–M.

---

## 4. Feature C — Image lifecycle on vind (P1)

**Pain addressed:** A4, A11, A10-warning.

### Task C1 — Investigate + fix `load-image` loss across `stop`/`start`
- **What:** reproduce: `load-image`, `stop`, `start`, check image presence. Determine whether vind discards the image store on stop or whether user-side `docker image prune` removed the only copy. Then either persist the image store across restarts or make `stop` warn ("locally loaded images may need reload; keep local copies until green").
- **Files:** `shoulders-cli/cmd/start.go`, `stop.go`, `app.go` (`load-image`/`build-image`), `shoulders-cli/internal/bootstrap/` (vind lifecycle), `1-cluster/` scripts.
- **Acceptance:** documented, reproducible behavior; no silent ImagePullBackOff after restart.
- **Priority / effort:** P1 / M (investigation first, fix second — do not assume).

### Task C2 — Build docs hardening (one-liners)
- **What:** quote `${img}:local` in all `build-image`/`load-image` loop examples (zsh `:l` modifier footgun, A11); add early ghcr-blocked warning for users who can't reach ghcr.io (A10).
- **Files:** website `build-image`/`load-image` guide pages, CLI `--help` examples.
- **Acceptance:** copy-pasted loop from docs works in zsh.
- **Priority / effort:** P1-docs / S.

---

## 5. Feature D — Laptop-medium survivability (P1, after CLI)

**Pain addressed:** A2, A3, AX1, AX2, A7, AX3.

### Task D1 — Realistic minimums per profile
- **What:** publish CPU / RAM / disk numbers per profile (`small`/`medium`/`large`): Docker Desktop allocation, free disk headroom, what each profile costs (Trivy, Falco, Loki/Tempo/Alloy, Kyverno ×4, Strimzi, control-plane). Include the data point from Bank (full 9-workload + 2×CNPG fits comfortably on 1-worker `small`) and the Astro datapoint (11 CPU / 11.7 GiB saturated on `medium`).
- **Files:** website getting-started + README profile table + `up --help`.
- **Acceptance:** a laptop user can decide `small` vs `medium` before paying the pull cost. Feeds Task A3 thresholds.
- **Priority / effort:** P1-docs / S.

### Task D2 — Break-glass runbook + safe defaults
- **What:** turn the Astro handover checklist into a real runbook page: webhook `Fail`→`Ignore` flip commands (×3 rounds, Flux reverts by design), `flux suspend helmrelease kyverno[-policies]` disambiguation (earlier suspends timed out ambiguously), `trivy-operator scale 0` + explicit resume rule ("keep at 0 on laptop medium; resume only on bigger iron"), stale `disk-pressure` taint clearing, CNPG finalizer/PVC last-resort steps, prune + `stop`/`start` recovery order. Evaluate safe defaults: Trivy/Falco opt-out or profile-gated on constrained laptops.
- **Files:** new website operations/break-glass page, `2-addons/profiles/` overlays if opt-outs land.
- **Acceptance:** on-call (or next agent) recovers without tribal knowledge; Trivy-resume collapse doesn't recur.
- **Priority / effort:** P1 / M (docs S, opt-out behavior M).

### Task D3 — StateStore resource knob (CNPG cpu/mem)
- **What:** add optional PostgreSQL compute requests/limits to the StateStore XRD + composition (propagated to the CloudNativePG Cluster), so demo DBs aren't BestEffort and don't die first on every disk-pressure wave (`-rw`/`-ro` endpoints emptying out). Keep dev-tier zero-config defaults.
- **Files:** `2-addons/manifests/crossplane/definitions/state-store-xrd.yaml`, `compositions/state-store-composition.yaml`, `shoulders-cli/cmd/infra.go` (`--tier` mapping extension), `pkg/api/v1alpha1/` types.
- **Acceptance:** `infra add-db --tier prod` (or new cpu/mem flags) yields CNPG instances with requests; BestEffort no longer the only option.
- **Priority / effort:** P1 / M.

### Task D4 — Multi-port WebApplications (deferred)
- **What:** support a second `port`/`service.port` (e.g. flagd OFREP 8016). Requires XRD + composition + CLI + HTTPRoute rule changes. Explicitly deferred: consumers tolerated the drop, single case so far.
- **Files:** `definitions/application-xrd.yaml`, `compositions/application-composition.yaml`, `cmd/app.go`.
- **Acceptance:** N/A until a second real case appears.
- **Priority / effort:** Future / L.

---

## 6. Feature E — Migration & concepts docs (P1, cheap, parallelizable)

**Pain addressed:** B3, B4, B6, B7, B8, B9, BX2, A9.

- **Task E1 — "Images & architectures" note** (B3): vind node arch, amd64-pin failure mode (`exec format error`), use `build-image`, avoid amd64 digests / amd64-only bases (`eclipse-temurin:17-jre-alpine` → `eclipse-temurin:17-jre`). Files: website concepts + `build-image` guide. P1-docs / S.
- **Task E2 — "Migrating existing apps" recipe** (B8 + Bank suggestions): workspace-prefix rename blast radius (`*_API_ADDR`, Service `:8080`→`:80` since `service.port` defaults to 80), secret-mount path remap, tracing off, DB URI rebuild from `<store>-rw` + app secret. Files: website guides/migration page. P1-docs / S.
- **Task E3 — "Migrating Jib apps" recipe** (B9): plain Dockerfile + multi-arch base for Jib-shaped Java apps (no Dockerfile, amd64 alpine Temurin pin). Same page as E2. P1-docs / S.
- **Task E4 — Tracing + OTel notes** (B4, A9): disable cloud tracing for local runs (`ENABLE_TRACING=false`); `OTEL_SDK_DISABLED` per-SDK matrix (Go/Python/.NET honor; Ruby crashes — needs per-service override; C++/PHP/Java-agent retry noisily → log volume → disk burn); verify each service. P1-docs / S.
- **Task E5 — `--init-sql` + credential docs** (B6, B7): multi-statement/file `--init-sql` example under StateStore concepts; explicit dev-tier credential defaults statement (`username=app` / `password=password`, change outside local dev). P1-docs / S.
- **Task E6 — "From CLI to GitOps" recipe** (BX2): one paragraph + commands — CLI for day-0/scaffolding (`--dry-run -o yaml`), committed manifests + `app apply -f` / `kubectl apply -f` (or Flux) after. P1-docs / S.

---

## 7. Explicitly deferred / not actionable

- **Brownfield importer (`app init --from deployment.yaml`)** (BX3) — Future. Large scope; the `--dry-run` + strip-server-fields escape hatch (Task B3) covers 80%.
- **`app doctor` required-env check** (A8) — parked. Own-error fast-caught via `shoulders logs`; low ROI.
- **Cold gRPC dial timeouts / `shoulders.io` API resets under load** (AX3) — observe. Document "retry once on cold dials" only if it reproduces; needs a second datapoint before control-plane tuning.
- **Upstream (non-Shoulders) bugs, recorded for provenance:** Bank `transactionhistory pom.xml` `Start-Class` typo (`TransActionHistoryApplication`); Astro `quote` Docker-Hub mlocati pin, `payment` ghcr OTel agent stage, `checkout|product-catalog|cart` ghcr grpc-health-probe stages. No platform tasks.

---

## 8. Suggested build order (this machine only, heavy last)

Validation machine: 11 CPU / 16 GiB / 100 GiB Docker Desktop. This meets the
`medium` minimums on paper (8+ CPU, 16 GiB mem, ~40 GiB disk per
CONTRIBUTING.md), but the reference datapoint stands: `medium` never converged
on an 11 CPU / ~12 GiB VM. The extra RAM + 100 GiB disk improve the odds, but
`medium` drills stay best-effort, last, with an abort rule (see §9.4).

1. B1 (panic fix) + A1/A2 (up hygiene) — Tier 0/1, correctness first.
2. B2/B3/B5 (command/args, dry-run, file mounts) — Tier 0/1, unblock migrations.
3. B4-light (probes, secret-items, ephemeral-storage, fsGroup) + D3-schema — Tier 0/1.
4. A3 (disk preflight, simulated-fill negative test) + A4 + D1 + E-docs — Tier 0/2.
5. C1 (image persistence, two `small` cycles) — Tier 3-first (slow but bounded).
6. D2 drill + D3 pressure-survival on `medium` — Tier 3-last, best-effort, abort rule applies.
7. D4 / importer / doctor — only on new evidence (no validation now).

---

## 9. Verification plan (per step)

### 9.1 Global gates (every code step)

- `go generate ./...` → `golangci-lint run --timeout 5m` → `go test -v ./...`
  (mirrors `test.yml:cli-test`).
- XRD/composition steps additionally require the Tier 1 live loop.
- Docs steps require link check (`docs.yml`) + verbatim copy-paste of every
  command block in `zsh`.

### 9.2 Validation tiers

- **Tier 0 — no cluster.** Unit tests, `--dry-run -o yaml` assertions (no API
  calls), docs link/copy-paste checks. Always runs on this machine.
- **Tier 1 — `small` live loop (the workhorse).**
  `shoulders down; shoulders up --local --set platform.profile=small` → edit →
  `shoulders sync` → `shoulders status --wait` green (reference: 37/37 pods) →
  `kubectl diff` clean. Single-workload deploys only.
- **Tier 2 — heavier single-machine, bounded.** Simulated near-full-disk
  negative test for A3 (small throwaway fill, then delete — never actually fill
  100 GiB); D1 table review; D3 schema-half on `small`.
- **Tier 3 — heavy, last, best-effort.** C1 (two full `small` cycles, slow but
  bounded) first; `medium` drills (D2 break-glass, D3 pressure-survival) last
  with the §9.4 abort rule.

### 9.3 Per-step validation

- **Step 1 — B1 + A1/A2 (Tier 0/1).**
  - B1: regression test in `cmd/app_test.go` — fake `spec` map through
    `applyAppFlagOverrides` with `--env`/`--env-from-*`/`--secret-mount`
    changed, then `unstructured.SetNestedMap` + deep-copy; assert no panic.
    Live: `app init e --image nginx --dry-run -o yaml` → apply →
    `app update e --env FOO=bar` succeeds; `app describe e` shows it.
  - A1: table test on the preflight helper (free / held-by-container /
    held-by-process). Live negative test: hold port 80 with a dummy container,
    run `up` → fails fast naming the squatter with the
    `down --name X` / `cluster list` remedy; no half-created cluster (Bank-1 repro).
  - A2: kill `up` mid-phase-1 twice → second `up` starts clean;
    kubeconfig never left as `clusters: null`.
  - A4: `LoadWithOverrides` doesn't mutate the file; `up --set ...` prints the
    "overrides are ephemeral" notice; config file unchanged post-run.
- **Steps 2–3 — B2/B3/B5 + B4-light + D3-schema (Tier 0/1).**
  - B2: `buildWebApplication` with flags → `spec.command/args` present; update
    path round-trips. Live: flagd-style deploy with `--command`/`--args`
    instead of a wrapper image; `kubectl diff` clean.
  - B3: dry-run prints without API calls (assert no client interaction).
    Live (Bank repro): `infra add-db foo --dry-run -o yaml` →
    `kubectl apply -f` → `kubectl diff` empty; re-apply is a true no-op.
  - B5: extend `TestBuildVolumesAndMounts` with configmap entries. Live: mount
    `otel-config.yml` from a ConfigMap with one flag; pod starts, no baked image.
  - B4-light: per-flag `--dry-run -o yaml` assertions (probes
    `initialDelaySeconds` 60–120 for Java; secret `items:` mapping;
    ephemeral-storage; `fsGroup`). Live on `small`: Java-shaped service goes
    Ready without probe-kill loops.
  - D3-schema: XRD validates; composition renders CNPG requests/limits on
    `small`. Pressure-survival half stays Tier 3.
- **Step 4 — A3 + D1 + E-docs (Tier 0/2).**
  - A3: threshold-table test (small/medium/large × available disk →
    allow/warn/deny). Live negative test with simulated fill:
    `up --set platform.profile=medium` refuses with required-vs-available
    numbers and the prune remedy instead of the Astro-3 death spiral.
  - D1: review check — a fresh reader can pick `small` vs `medium` without
    asking; numbers match the CONTRIBUTING.md cluster-sizes section (update
    both in the same PR to avoid drift).
  - E-docs (E1–E6): each page's commands pasted verbatim in `zsh` on this
    machine (arm64 rebuild, Jib Dockerfile, `--init-sql` file example,
    `${img}:local` loops). Second-reader test: could the Bank/Astro reporter
    have avoided their detour?
  - C2 one-liners ship here: docs loop examples use `${img}:local`; ghcr
    warning present; `--help` examples match.
- **Step 5 — C1 (Tier 3-first).**
  - Repro matrix on `small`: `load-image` → `stop` → `start` → image presence
    check. Record whether vind discards the image store or user-side prune
    removed the only copy. Acceptance: documented, reproducible behavior; no
    silent ImagePullBackOff. If a fix lands, re-run the matrix green and verify
    the `start` re-push / `sync`-restores path.

### 9.4 Tier 3-last: `medium` drills (best-effort, abort rule)

- **What:** D2 break-glass drill + D3 pressure-survival. Attempt on this
  machine **once**, last. Requires `medium`
  (Kafka 3 brokers + Loki/Tempo/Alloy + Trivy/Falco + Policy Reporter).
- **D2 drill:** induce pressure → follow only the runbook (webhook
  Fail→Ignore flips noting Flux reverts by design, `flux suspend`
  disambiguation, Trivy `scale 0` + resume rule, taint clearing, CNPG
  last-resort, prune/stop/start order) → recover without tribal knowledge.
- **D3 pressure-survival:** `infra add-db --tier prod` (or cpu/mem flags) →
  `kubectl describe` CNPG cluster shows requests; instances not BestEffort;
  `-rw`/`-ro` endpoints survive a wave that previously emptied them. Run the
  airgap bundle loop (`vendor` + `up --bundle` on `small`) if chart inventory
  was touched.
- **Abort rule:** if `kubectl describe nodes` shows DiskPressure/memory pressure
  within ~15 min of converge, stop, record "machine-bound, needs devserver/CI",
  keep the runbook docs-only. A red `medium` status never blocks or
  reinterprets Tier 0–2 results.
- **Outcome (2026-09-27, 11 CPU / 16 GiB / 100 GiB):** drill executed, abort
  rule not triggered. `up --local --set platform.profile=medium` converged in
  ~14 min (64/64 pods, zero pressure) after fixing a P0 found by the drill
  itself: the default profile pinned a nonexistent vind k8s `v1.37.0` image
  (renovate #43; small/large were on `v1.36.0`), so every default `up` failed
  at cluster creation — pinned back to `v1.36.0`. D2 verified: webhook
  Ignore→Fail round-trip (something reverted to Fail by itself, as the runbook
  says), Trivy scale 0→1 resume. D3 verified: `--tier prod` → 2 CNPG instances
  with 250m/512Mi requests, `-rw`/`-ro` endpoints populated. Not exercised: a
  real pressure wave (nothing to survive it with on a green cluster), and the
  full D2 sequence on a genuinely wedged cluster. Also observed one transient
  API `connection reset` (cf. AX3).

### 9.5 Deferred items (no validation now, entry criteria only)

- D4 multi-port, brownfield importer, `app doctor`: build only on a second
  real case.
- AX3 (cold gRPC dials / API resets under load): document "retry once on cold
  dials" only if it reproduces; needs a second datapoint before control-plane
  tuning.
