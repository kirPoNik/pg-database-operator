# build-log

## 2026-09-20 — Repo bootstrap + Phase 1 (baseline running)

**Goal for this session:** finish the `Background` section of `docs/brief.md`, then get Phase 1 done: Postgres running in `kind`, `PgDatabase` CRD scaffolded, controller connects to Postgres on startup and logs the server version, no reconcile logic yet.

### Repo bootstrap

- Created the GitHub repo `kirPoNik/pg-database-operator` (public, personal account) with `gh repo create --public --source=. --remote=origin`, pushed the existing `main`.
- Applied branch protection to `main`: `allow_force_pushes: false`, `allow_deletions: false`, no required reviews (solo repo, direct pushes still allowed). Confirmed via `gh api repos/kirPoNik/pg-database-operator/collaborators` that `kirPoNik` is the only collaborator — GitHub only grants push access to invited collaborators regardless of branch protection, so that alone already meant nobody else could push.

### kubebuilder scaffold vs. the existing repo layout

**What we tried:** running `kubebuilder init` and `kubebuilder create api` directly in the repo root.
**What we did instead:** scaffolded in a scratch directory first (`kubebuilder init --domain pglab.dev --repo github.com/kirPoNik/pg-database-operator --owner "Kirill Polishchuk" --license none`, then `kubebuilder create api --group lab --version v1alpha1 --kind PgDatabase --resource --controller`), then merged by hand — because kubebuilder unconditionally generates its own `Makefile`, `README.md`, `LICENSE` (Apache-2, even with most license flags), and `.gitignore`, all of which would have clobbered the repo's existing versions (`README.md`, MIT `LICENSE`, and the working-agreement `.gitignore` entries like `docs/raw/*.tmp`).
**Why this matters for anyone repeating this:** `--license none` avoids the Apache-2 boilerplate on generated `.go` files but does **not** stop kubebuilder from writing its own Makefile — that always has to be merged by hand if the repo already has one.
- Merge result: kept kubebuilder's generated Makefile (all the `manifests`/`generate`/`test`/`deploy` machinery) as the base, added a `##@ Lab` section on top with `setup`, `run` (overridden to point at `localhost:$(HOST_PG_PORT)`), `experiment`, `nuke-connections`, `teardown`.
- The scaffold's own directory name (the scratch dir was named `kb-scaffold`) leaked into every generated manifest (`kb-scaffold-system` namespace, `kb-scaffold-test-e2e` kind cluster name, etc.) via kubebuilder's directory-name-based project-name inference. Caught this with `grep -rl kb-scaffold .` after copying files in, fixed with a blanket `sed -i '' 's/kb-scaffold/pg-database-operator/g'` across every matched file. **Lesson:** always scaffold in a scratch dir whose name is the real project name, or grep for the scratch dir's name immediately after copying.

### Dependency choice: pgx over lib/pq

Brief listed `pgx` or `database/sql` + `lib/pq` as options. Went with `github.com/jackc/pgx/v5`, used through its `database/sql` adapter (`pgx/v5/stdlib`) rather than `pgxpool` directly, specifically so the controller gets a plain `*sql.DB` with `SetMaxOpenConns` — matching the brief's stuck-risk-2 fallback ("a single shared pooled `*sql.DB` with an explicit `SetMaxOpenConns` from the start") literally, from Phase 1, even though nothing stresses the pool yet. `lib/pq` was ruled out: it's in maintenance-only mode, `pgx` is the actively maintained, most popular option (org dependency policy: avoid unmaintained deps).

### Dead end: Postgres image wouldn't pull inside the kind node

**What we tried:** `kubectl apply -f deploy/postgres.yaml` (Namespace + Service + StatefulSet referencing `postgres:16.4`), then waited on `kubectl -n postgres rollout status statefulset/postgres`.
**What broke:** rollout timed out. `kubectl -n postgres describe pod postgres-0` showed:
```
Warning  Failed  Failed to pull image "postgres:16.4": failed to pull and unpack image
"docker.io/library/postgres:16.4": failed to resolve reference "docker.io/library/postgres:16.4":
failed to do request: Head "https://registry-1.docker.io/v2/library/postgres/manifests/16.4":
tls: failed to verify certificate: x509: certificate signed by unknown authority
```
**Diagnosis:** the kind node is a separate container with its own containerd and its own CA trust store, distinct from the host Docker (Colima) daemon. `docker pull postgres:16.4` on the host worked instantly — so the host's trust chain is fine, but the kind node's isn't (this looks like a corporate/network TLS-interception cert the host trusts and the node doesn't).
**First fix attempt that also failed:** `kind load docker-image postgres:16.4 --name pg-lab` — different error:
```
ERROR: failed to load image: command "docker exec --privileged -i pg-lab-control-plane ctr --namespace=k8s.io
images import --all-platforms --digests --snapshotter=overlayfs -" failed with error: exit status 1
Command Output: ctr: content digest sha256:9a70e4d1c03a5066080292db2dd95ee3965d3651316e21989fa0935afb8ce8ca: not found
```
Same error with `docker save` + `kind load image-archive` (that path also shells out to `ctr images import --all-platforms` internally) — so it isn't a `docker load` vs `docker save` difference, it's specifically the `--all-platforms` flag choking on a multi-arch manifest list where only the local (arm64) platform's layers actually exist locally.
**What actually fixed it:** import the same tar manually, without `--all-platforms`:
```
docker save postgres:16.4 -o postgres-16.4.tar
docker exec -i pg-lab-control-plane ctr --namespace=k8s.io images import --digests --snapshotter=overlayfs - < postgres-16.4.tar
kubectl -n postgres delete pod postgres-0 --wait=false   # force a re-schedule now the image is cached on the node
```
This is not scripted into the Makefile yet — `make setup` still just does a plain `kubectl apply`, so on a machine without this TLS problem it should work unmodified. If `make setup` hangs on `ImagePullBackOff`, this is the fix.

**Surprised me:** that `kind load docker-image` — kind's own documented mechanism for exactly this situation (get a locally-available image into a kind node without a registry round-trip) — fails on this host for a reason unrelated to the TLS problem it's normally used to route around. The multi-platform import path (`--all-platforms`) is not optional in kind's own loader, so on any host where the local Docker image only actually contains one platform's layers (normal after a plain `docker pull` on Apple Silicon), `kind load` can fail even though a manual `ctr images import` of the identical tar succeeds.

### What's not built yet (honest limitations after Phase 1)

- No reconcile logic — the `PgDatabaseReconciler` is the bare kubebuilder scaffold, `Reconcile()` does nothing. Phase 2.
- Controller runs on the host (`make run`), not yet deployed into the cluster as a Deployment (`make deploy` exists, from kubebuilder, but untested against this project's manager RBAC yet).
- `make setup` does not yet automate the image-import workaround above — it's a manual step if you hit the same TLS issue.
- `kubectl` client (v1.34.1) and the kind node's Kubernetes server (v1.36.1) differ by two minor versions, past kubectl's documented +/-1 skew support. Nothing has broken because of this yet, but it's untested territory — see `docs/environment.md`.

## What never worked

