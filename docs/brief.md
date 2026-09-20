# A control plane for something that is not Kubernetes: reconciling Postgres databases with no watch API

**Repo slug:** `pg-database-operator`
**Pattern:** A1 — control planes and reconciliation, with A6 (data layer internals)
**Surface:** local `kind` + Postgres + Go, benchmarked against Crossplane `provider-sql`
**Effort:** full weekend (the biggest of the three)

---

**Competencies:**
- *Declarative APIs over systems that do not push events* — "how do you build a reconcile loop when the thing you manage has no watch, only queries?"
- *Polling economics* — "what does drift detection cost when every check is a query against the system you are managing?"
- *Deletion as a first-class problem* — "the external resource cannot be deleted right now. What does your finalizer do, and for how long?"
- *Build vs adopt* — "there is already a provider for this. Defend writing your own, or defend not writing it."

**Where this shows up:** Crossplane providers, Cluster API infrastructure providers, cloud operators, every internal "database as a service" platform, and every team that has ever wrapped a vendor API in a CRD. In interviews it is the "design a declarative API for X" question, where X is deliberately not Kubernetes.

**Maturity:** the Kubernetes side is `kubebuilder`, which is as settled as it gets. The comparison target, `crossplane-contrib/provider-sql`, has managed resources for PostgreSQL `Database`, `Role`, `Grant` and `Extension`, and a documented install path. It also has real open issues — which is useful, not a problem: a known limitation in a mature project is exactly the kind of evidence a build-vs-adopt ADR needs.

## Background — the concept explained from scratch

### 1. What it actually is, from first principles

Strip away Kubernetes-specific vocabulary and this project is one instance of a much older idea: a **control loop**. A control loop has three parts — it observes the current state of something, compares that to a desired state, and takes an action that nudges the current state closer to desired. It repeats forever. A thermostat is a control loop (observe temperature, compare to setpoint, turn the furnace on or off). A Kubernetes controller is a control loop where the "something" is an object in the cluster (a `Deployment`, a `Pod`) and the actions are API calls back to the cluster.

This project builds one specific control loop: the desired state is a `PgDatabase` custom resource (a YAML object you `kubectl apply`), and the real-world thing being steered is a **logical database inside a Postgres server** — a `CREATE DATABASE` / `CREATE ROLE` / `CREATE EXTENSION` away from existing. The controller's job is to keep "what Postgres actually has" in sync with "what the `PgDatabase` object says it should have," forever, without a human running SQL by hand.

What makes this project specifically interesting, rather than a kubebuilder tutorial re-run, is *how* the controller finds out that reality has drifted from desired state. For a native Kubernetes object, the API server tells the controller the instant something changes (a **watch**, pushed over a long-lived HTTP connection). Postgres has no equivalent mechanism for "tell me when a database is dropped." The controller can only find out by asking — running a query — on some schedule it picks itself. That's the entire subject of the project.

### 2. The problem it solves

In the small, this project answers a mundane operational problem: someone wants a new Postgres database and role provisioned for a service, on demand, without a human logging into `psql` and running DDL, and without secrets pasted into a Slack message. That's "database as a service," and it's the same problem every internal platform team eventually gets asked to solve for *some* external system — Postgres today, an S3 bucket or a DNS record or a SaaS API key tomorrow.

In the large, it's a specific instance of the **build vs. adopt** problem for infrastructure automation: `crossplane-contrib/provider-sql` already does roughly this. Before writing a bespoke operator for anything, a staff engineer should be able to articulate precisely what the existing tool costs you (an extra control plane to run, its own failure modes, its own release cadence, real open bugs) against what building costs you (all of that logic, tested, forever, in-house). This project is built specifically so that answer can be backed by first-hand measurements instead of a hunch.

### 3. How it works mechanically

Two different synchronization mechanisms are stacked on top of each other here, and separating them is the key to understanding the whole design:

1. **`PgDatabase` object → controller.** This part *does* have a watch. The controller registers an **informer** with `kube-apiserver`, which keeps a local cache of `PgDatabase` objects in sync via a long-lived watch connection and pushes the object's key onto an in-memory **work queue** the instant it's created, changed, or deleted. This part is "free" — it is exactly the same machinery every Kubernetes controller uses for Pods and Deployments, and it is instant.
2. **Controller → Postgres.** This part has no watch. The controller cannot ask Postgres to notify it when `DROP DATABASE` happens. So after every reconcile, the controller tells the work queue "run me again for this object after N seconds" (`ctrl.Result{RequeueAfter: N}`). That's a **poll**, dressed up in Kubernetes clothing. `N` is the one knob that trades detection latency against query load — the central tension the experiment in this brief measures.

Inside a single reconcile, the shape is always **observe, then act, then report**:
- *Observe*: run catalog queries against Postgres system tables (`pg_database`, `pg_roles`, `pg_extension`) to find out what actually exists right now.
- *Act*: diff observed state against the `PgDatabase` spec, and issue only the SQL needed to close the gap (`CREATE ROLE`, `CREATE DATABASE`, `CREATE EXTENSION`, or nothing at all if nothing has drifted). Every action must be safe to run repeatedly — see **idempotency** in the vocabulary below.
- *Report*: write the outcome back onto the `PgDatabase`'s `status` subresource (conditions like `Ready`, a `lastObservedTime`) and, on first creation, write generated credentials into a Kubernetes `Secret`.

Deletion is handled by the same loop but needs one extra piece of Kubernetes machinery, a **finalizer**, precisely because `DROP DATABASE` can fail (another session is connected) and Kubernetes otherwise has no concept of "deletion that might not finish immediately." Without a finalizer, `kubectl delete` would remove the object right away regardless of whether the real database was ever actually dropped.

### 4. Vocabulary

| Term | Means, in this project |
|---|---|
| **CRD** (Custom Resource Definition) | The schema you register with Kubernetes so it will accept `PgDatabase` objects at all. |
| **CR** (Custom Resource) | One instance — one `PgDatabase` YAML object, `kubectl apply`-ed. |
| **Controller / Operator** | The Go program that watches CRs and reconciles them. "Operator" usually implies it also encodes operational knowledge (e.g., how to safely provision a database), not just CRUD. |
| **Reconciler / `Reconcile()`** | The function called with an object's key every time it might need attention. Must be safe to call for no reason (nothing changed) — that will happen constantly. |
| **Informer / watch** | The mechanism that keeps the controller's view of `PgDatabase` objects (and any other native k8s object it reads) live, pushed by `kube-apiserver`. Exists for k8s objects; does **not** exist for Postgres. |
| **Poll** | Actively asking "has anything changed?" on a timer, because nothing will tell you. What the controller must do against Postgres. |
| **`RequeueAfter`** | The return value that schedules the next poll for a given object. This is the poll interval, and it's per-reconcile, so it can differ per object. |
| **Drift** | Reality (what's actually in Postgres) diverging from desired state (the `PgDatabase` spec) without the controller having caused it — e.g., someone runs `DROP DATABASE` by hand. |
| **Idempotency** | A reconcile that produces the same end state no matter how many times, or in what partially-failed order, it runs. `CREATE ... IF NOT EXISTS` is a syntax trick, not a substitute for actually checking observed state first — see misconception #2 below. |
| **Finalizer** | A string on an object's metadata that tells `kube-apiserver` "don't actually delete this object until I remove my finalizer." Used here to hold the `PgDatabase` object in a `Terminating` state until `DROP DATABASE` genuinely succeeds (or the deletion policy says to give up and orphan it). |
| **`deletionTimestamp`** | Set by the API server the moment `kubectl delete` runs on an object with a finalizer present. The object still exists and is still returned by `get`/`watch` — it's just marked for death — until every finalizer is removed. |
| **Managed resource** | Crossplane's term for a CR that represents something outside the cluster (their `Database`, `Role`, etc. are managed resources — the same concept as this project's `PgDatabase`, from a different vendor). |
| **`ProviderConfig`** | Crossplane's way of telling a provider *which* external Postgres to talk to and with what credentials, sourced from a k8s `Secret`. |
| **Status condition** | A structured, machine-readable entry in `.status.conditions` (type, status, reason, message) — the standard k8s way to report "is this object actually in the state it claims," as opposed to just logging it. |
| **Backoff** | The (usually exponential) growing delay between retries after a reconcile *fails*, distinct from `RequeueAfter`, which schedules the *next planned* poll after a *successful* one. |

### 5. What to understand first

Read in this order if any of the above still feels shaky:
1. **Kubernetes objects and the declarative model** — what `kubectl apply` actually does (it's a diff-and-patch against desired state stored in etcd, not an imperative "run this command"). If project 1 in this lab series (a plain kubebuilder CRUD operator) is done already, this is covered.
2. **The generic controller/reconcile pattern**, independent of what's being managed — the [Kubernetes "Operator pattern" doc](https://kubernetes.io/docs/concepts/extend-kubernetes/operator/) and the kubebuilder book's "What is a Reconciler" section are both short.
3. **Postgres roles, ownership, and catalogs** — specifically, that a "database" and a "role" are separate objects with an owner relationship, that `pg_database`/`pg_roles`/`pg_extension` are queryable system catalogs, and that connections are always *to* one specific database, not to the server generally.
4. **Level-triggered vs. edge-triggered thinking** — the single most important mental model for this whole project (see #6 and #7 below). A reconcile must work correctly even if it is triggered late, early, twice, or after missing several intermediate states entirely, because with polling that will happen.

### 6. Related and adjacent topics

- **Crossplane and the "provider" pattern generally** — Crossplane is a framework for writing exactly this kind of controller for arbitrary external APIs; `provider-sql` is one provider among many (AWS, GCP, Azure, Helm, and dozens more all follow the same managed-resource shape).
- **Terraform and other apply-time reconcilers** — a genuinely useful contrast, not a competitor to read past. Terraform reconciles too, but only when you run `terraform apply`; it has no standing control loop and therefore no polling cost at rest, at the price of never noticing drift until you next run it. Comparing "continuous but costs queries" (this project) against "free at rest but blind between runs" (Terraform) is a clean way to explain the tradeoff to someone who already knows Terraform.
- **Purpose-built Postgres-in-Kubernetes operators** — `CloudNativePG` and the Zalando `postgres-operator` solve a *different* problem that's easy to conflate with this one: they manage the Postgres **server itself** running as pods in the cluster (replication, failover, backups). This project instead manages logical databases *inside* a Postgres server that may not be in Kubernetes at all. Worth knowing they exist so the distinction is easy to state.
- **Control theory / control loops as a general distributed-systems pattern** — reconciliation, eventual consistency, and level-triggered control are the same underlying idea showing up in configuration management (Puppet/Chef "converge" runs), GitOps (`Flux`/`Argo CD` reconciling a cluster against a git repo), and classical control systems engineering.
- **`client-go`'s `sample-controller`** — the reference implementation of the informer/work-queue pattern kubebuilder builds on top of, worth a skim once the generated code stops feeling like magic.

### 7. Common misconceptions, corrected

- **"The controller watches Postgres."** No — it watches the `PgDatabase` *object* in Kubernetes. Nothing watches Postgres; the connection to Postgres is a plain SQL client making queries on a timer chosen by the controller.
- **"`CREATE DATABASE IF NOT EXISTS`-style guards make a reconcile idempotent."** They make individual *statements* safe to repeat, but that's not the same as a correct reconcile. Idempotency here means: given any observed state, running `Reconcile()` produces the same converged end state — including detecting and fixing partial drift (e.g., the database exists but an extension was dropped), not just "was it missing entirely." A reconcile that only checks "does the database exist at all" and stops there will silently ignore every other kind of drift.
- **"A shorter poll interval is strictly better — it's just a latency/cost dial with no downside besides cost."** True as far as it goes, but the actual finding this project is built to surface is that the cost is not a flat tax: query and connection load scale **linearly with the number of managed resources**, so a poll interval that's cheap for 5 databases can be a meaningful fraction of total load on the Postgres server at 500. It is a per-fleet cost, not a per-controller one.
- **"Postgres's `DROP DATABASE ... WITH (FORCE)` always succeeds in terminating blockers."** The docs are explicit that `FORCE` does *not* terminate connections that are in a prepared transaction, or backing an active logical replication slot or subscription. A finalizer that assumes `FORCE` is unconditional will hang exactly in that case — which is realistic, not a corner case, on any database that uses logical replication.
- **"A finalizer that can't complete blocks forever by default, and that's a bug in Kubernetes."** It's a deliberate design choice, and it's *your* controller's choice, not the platform's. Kubernetes will hold the object in `Terminating` for as long as your finalizer string remains on it — indefinitely, if that's what your code does. The design decision this project asks you to make explicitly (refuse-and-report, time-out-and-orphan, or block forever) is exactly this: what should *your* code do when it can't finish.
- **"Since Crossplane already solved this, there's nothing to learn by building it yourself."** Crossplane's own documentation says managed resources also only poll for external drift (default one minute, `provider-sql` specifically backs off to ten minutes to protect the database) — meaning the interesting engineering problem in this brief is not solved by "just use Crossplane," it's *faced by* Crossplane too. Adopting it changes who is responsible for the tradeoff, not whether the tradeoff exists.

### 8. Where to read more

| Source | What it is good for | Time |
|---|---|---|
| **Kubernetes docs — "Operator pattern"**<br>https://kubernetes.io/docs/concepts/extend-kubernetes/operator/ | The shortest official statement of the control-loop idea this whole project is an instance of. Read this first if the CRD/controller/operator vocabulary is new. | 10 min |
| **The kubebuilder book**<br>https://book.kubebuilder.io/ | The step-by-step companion to the actual scaffolding commands used in Phase 1 (`kubebuilder init`, `kubebuilder create api`), including the generated project layout and the `Reconcile` contract. | 30 min to skim, reference throughout |
| **Crossplane docs — Crossplane Pods**<br>https://docs.crossplane.io/latest/guides/pods/ | The clearest official statement of the constraint this whole project is about. **Backs the claims in this brief** that managed resources use a Kubernetes watch for spec and deletion events but rely on **polling** to detect changes in the external system; that the default poll rate is one minute, changed with `--poll-interval` or overridden per resource with the `crossplane.io/poll-interval` annotation; that `crossplane.io/reconcile-requested-at` forces an immediate reconcile; and that a separate global sync re-checks everything hourly by default. | 20 min |
| **`crossplane-contrib/provider-sql` — README**<br>https://github.com/crossplane-contrib/provider-sql | The comparison target's install path and its own stated tradeoff. **Backs the claims** that it reconciles managed resources every 10 minutes specifically to reduce load on the managed databases, that PostgreSQL support covers `Database`, `Role`, `Grant` and `Extension`, and that a `ProviderConfig` reads username, password, endpoint and port from a Kubernetes Secret. Check the current package tag and registry against the README before installing — the registry moved. | 20 min |
| **`crossplane-contrib/provider-sql` — issue #240, "(postgresql) Grant privileges on database — external resource existence never confirmed"**<br>https://github.com/crossplane-contrib/provider-sql/issues/240 | A real, documented case where a managed resource never reaches ready state because the validation query expects an exact match against the granted privilege set. This is the single most useful citation in the build-vs-adopt ADR: it is concrete evidence about how hard "observe the external state correctly" actually is. | 10 min |
| **PostgreSQL docs — DROP DATABASE**<br>https://www.postgresql.org/docs/current/sql-dropdatabase.html | The mechanics behind the deletion experiment. **Backs the claim** that the command fails while anyone else is connected to the target database, and that it cannot run inside a transaction block. Read the `FORCE` option carefully — it attempts to terminate existing connections, but the docs state it does not terminate when prepared transactions, active logical replication slots or subscriptions are present. That exception is exactly where a finalizer that assumes `FORCE` always works will hang. | 10 min |
| **PostgreSQL docs — `pg_stat_activity` and `pg_stat_statements`**<br>https://www.postgresql.org/docs/current/monitoring-stats.html | How the query-load and connection-count measurements in this project actually get taken. Worth reading before Phase 3, not needed for Phase 1. | 15 min |
| **Kubernetes blog — "Using Finalizers to Control Deletion" (14 May 2021)**<br>https://kubernetes.io/blog/2021/05/14/using-finalizers-to-control-deletion/ | The deletion mechanics your finalizer sits on: `deletionTimestamp`, why the object stays visible, and who is responsible for removing the finalizer. Read before writing phase 2 step 4, because getting this wrong leaves objects stuck in a way that is annoying to clean up in a shared cluster. | 20 min |
| **`client-go` — `sample-controller`**<br>https://github.com/kubernetes/sample-controller | The reference implementation of the informer + work-queue pattern kubebuilder generates on top of. Useful once the generated code in `internal/controller/` stops feeling self-explanatory and you want to see the primitives underneath. | 30 min, optional |

Link text (repo names, doc titles) above was written from general knowledge of these well-established, stable projects; verify each URL still resolves before citing it in the published article, since this brief itself has not re-checked them live.

---

### The system

A CRD, `PgDatabase`, describing a logical database inside a Postgres instance: database name, owner role, required extensions, and a deletion policy. A controller reconciles it against a real Postgres server: creates the role, creates the database, installs extensions, writes the generated credentials into a Secret, and reports observed state in `status`.

Postgres does not push change events. There is no watch. So the controller must **poll** — and every poll is a query against the database you are managing. That constraint is the whole project.

```mermaid
flowchart LR
    U[kubectl apply PgDatabase] --> API[kube-apiserver]
    API -->|watch| C[PgDatabase controller]
    C -->|RequeueAfter poll| C
    C -->|SQL: catalog queries,<br/>CREATE ROLE / DATABASE| PG[(Postgres)]
    C -->|write Secret + status| API
    PSQL[psql: DROP DATABASE<br/>ALTER ROLE — drift] --> PG
    C -.no watch available.-x PG
```

The dotted line is the design problem: the controller can never be told that something changed. It can only ask, on a schedule it chooses.

---

### Stack

| Tool | Why this one |
|---|---|
| `kind` + Postgres (a StatefulSet, or Docker Compose alongside) | Postgres is the lab's stateful anchor for the whole year. Put it in the cluster now and reuse it every month after. |
| `kubebuilder` (latest v4) + Go (v1.24.6+ per the Quick Start) | Same scaffold as project 1. |
| `pgx` or `database/sql` + `lib/pq` | One pooled `*sql.DB` per Postgres endpoint. Pooling matters here — see stuck-risk 2. |
| Crossplane + `crossplane-contrib/provider-sql` | The comparison target. Install via a `Provider` manifest and a `ProviderConfig` pointing at a Kubernetes Secret holding username, password, endpoint and port. |
| `pg_stat_activity` / `pg_stat_statements` | How you measure what polling actually costs the database. |

---

### Build phases

**Phase 1 — baseline running (≤2h)**

1. Postgres running in `kind`, reachable, with a superuser secret.
2. `kubebuilder create api --group lab --version v1alpha1 --kind PgDatabase`.
3. Controller connects to Postgres on startup and logs the server version. No reconcile logic yet.

**Done when:** `make run` prints the Postgres version it read over SQL.

**Phase 2 — the reconcile loop (1–2 evenings)**

1. `Reconcile` reads desired state, then **observes** actual state with catalog queries (`pg_database`, `pg_roles`, `pg_extension`).
2. Create what is missing. Make every operation idempotent — check first, then act, and never rely on `IF NOT EXISTS` alone to hide a logic bug.
3. Generate a password, write it to a Secret, and never log it.
4. Add a finalizer. On delete, honour `spec.deletionPolicy`: `Delete` drops the database, `Orphan` leaves it and just removes the finalizer.
5. Return `RequeueAfter` to set the poll interval, and make it configurable.
6. Set status conditions: `Ready`, plus `lastObservedTime`.

**Done when:** you `DROP DATABASE` by hand in `psql` and the controller recreates it without you touching the CR.

**Phase 3 — the experiment (half day)**

Install Crossplane and `provider-sql`, point it at the same Postgres, and create the equivalent `Database` and `Role` resources. Then run the same drift against both.

---

### The experiment

**What I measure**

1. **Drift detection latency vs poll interval.** Drop the database in `psql`; time until it exists again. Your controller at `RequeueAfter` = 10s, 1m, 10m. `provider-sql` at its default and at a lowered interval. Ten runs per cell.
2. **Query load on the managed database**, per managed resource per hour, at each poll interval. Count from `pg_stat_statements`, or sample `pg_stat_activity`. This is the number nobody publishes and it is the heart of the article.
3. **Connection behaviour under fan-out.** Create 50 `PgDatabase` objects at a 10-second interval. Watch connection count. Find the point where the controller becomes a load generator against the thing it is supposed to be managing.
4. **Deletion under contention.** Delete a `PgDatabase` while a client holds an open connection to that database, and time how long the finalizer blocks.
5. **Behaviour when Postgres is unreachable.** Stop Postgres mid-reconcile. Record: what `status` says, how the backoff behaves, and whether the controller hot-loops.

**Expected result**

Detection latency tracks the poll interval almost exactly, because there is nothing else that can trigger a check. Query load scales linearly with (number of resources ÷ poll interval), which sounds obvious written down and is routinely forgotten in design reviews.

**What would be surprising — and this is the article**

Three candidates, and at least one of them will land.

- **The deletion deadlock.** Postgres refuses to drop a database that has active connections. A finalizer that retries `DROP DATABASE` forever leaves the Kubernetes object stuck in `Terminating` with no error visible to whoever ran `kubectl delete`. That is a genuinely nasty production failure mode, it is easy to reproduce on purpose, and the fix is a design decision rather than a code fix. Modern Postgres offers a `FORCE` option that attempts to terminate existing connections — but the docs are explicit that it does not terminate when prepared transactions, active logical replication slots or subscriptions are present. So the real question is what your controller does when even `FORCE` fails: refuse and surface a condition, time out and orphan, or block forever. Pick one on purpose and defend it in the ADR.
- **The polling cost crossover.** There is an interval below which your control plane is a meaningful fraction of the load on the database it manages. Finding that number for a trivial workload — and pointing out that it scales with resource count, not with change rate — is the whole argument for event-driven external systems.
- **The comparison is not the one you expect.** `provider-sql` reconciles managed resources on a ten-minute cycle by default. Crossplane's own docs are explicit that managed resources watch Kubernetes for spec changes but rely on **polling** to detect changes in the external system, at a default poll rate of one minute, adjustable with `--poll-interval` or a per-resource annotation, with an additional global sync every hour. So the honest comparison is not "mine is faster" — it is that both implementations face the identical constraint and the interesting engineering is entirely in what you do about it.

---

### Failure modes to induce

- `DROP DATABASE` behind the controller's back (baseline drift).
- `ALTER ROLE ... NOLOGIN` behind the controller's back — drift in a field you may not even be checking.
- Delete the CR while a connection to that database is open (the finalizer deadlock).
- Stop Postgres entirely during a reconcile.
- Create 50 resources at a 10-second interval until the connection pool or `max_connections` gives out.
- Point two `PgDatabase` objects at the same physical database name and see which one wins.
- Restore a database by hand while the controller thinks it is absent, mid-create.

---

### Artifact

- Public repo `pg-database-operator`.
- **Table 1:** drift detection latency × poll interval × implementation (yours vs `provider-sql`).
- **Table 2:** queries and connections against Postgres per managed resource per hour, by interval.
- **Table 3:** deletion outcomes — clean, blocked, orphaned — with the time each took.
- **Mermaid diagram:** the observe-then-act loop and where the missing watch forces a poll.
- **`artifact-adr.md`:** write an operator, or adopt an existing provider, for an external system with no watch API.

---

### Prerequisites and stuck-risk

**You must already know:** Go, SQL and Postgres administration (roles, grants, catalogs), and CRDs. Doing project 1 first makes this much smoother, because the scaffold and the reconcile mechanics are then not new.

**Stuck-risk 1 — getting Crossplane and `provider-sql` wired up in `kind`.** The chain is Provider install → provider revision becomes active → `ProviderConfig` referencing a Secret with the right key names → managed resources. Any link can quietly not happen, and there is a known open issue where a Postgres `Grant` never reaches ready state because the privilege comparison expects an exact match against the granted set.
*Fallback:* drop the live comparison. Measure your own controller across all intervals, and compare against `provider-sql`'s **documented** behaviour, cited and dated, with an honest note that you did not run it. The article survives this completely — most of its value is in your own numbers. Do not spend the weekend fighting the install.

**Stuck-risk 2 — connection exhaustion turns into a blocked laptop rather than a result.** At a 10-second interval across 50 resources, a naive one-connection-per-reconcile design will hit `max_connections` and lock you out of `psql` too.
*Fallback:* use a single shared pooled `*sql.DB` with an explicit `SetMaxOpenConns` from the start, and add a `make nuke-connections` target that runs `pg_terminate_backend` so you can always recover. Then treat exhaustion as a **measured result** you produce on purpose, not an accident you recover from.

**Stop line — the minimum publishable result:** your controller creating and dropping one database and role, detecting one class of hand-made drift, with the poll-interval × detection-latency × query-load table for your own implementation, plus the deletion deadlock reproduced once. The Crossplane comparison is a bonus, not a requirement.

---

### The article that falls out

**Title:** "Building a control plane for something that cannot be watched: reconciling Postgres from a Kubernetes CRD"

**Hook:** Kubernetes controllers feel instant because everything inside Kubernetes pushes events. Point one at a database and that illusion disappears immediately.

**Spine:**
1. Why the reconcile pattern still applies to systems with no watch, and what changes when it does.
2. Observe, then act: writing an idempotent reconcile against SQL catalogs.
3. The polling bill — detection latency and query load as two ends of the same dial.
4. Deletion is the hard part: the finalizer that could not drop the database.
5. Build or adopt — what an existing provider gives you, and what it costs you.

---

### How it compounds

Leaves Postgres running in the lab as the permanent stateful anchor, with a controller that can provision databases on demand — which is directly reusable in month 3 (outbox and change data capture), month 6 (multi-tenancy, where per-tenant databases become the isolation unit) and month 10 (high-load architecture). Extends project 1 by taking the same pattern outside Kubernetes, which is the step that turns "I can write an operator" into "I can design a control plane".

---

### Handoff notes

**Repo:** `pg-database-operator` (public, personal account, MIT). Synthetic data only; the Postgres instance is a throwaway in `kind`.
**Staff artifact:** `docs/artifact-adr.md` — build vs adopt for external-system control planes.

**Must be captured in `docs/build-log.md` while building:**
- The exact SQL error text when `DROP DATABASE` is refused, and what the Kubernetes object looked like at that moment (`kubectl get -o yaml`, including finalizers and deletionTimestamp).
- Every place you discovered the reconcile was not idempotent, and how you found out.
- The full Crossplane/`provider-sql` install sequence including anything that did not work, with error text — this is the single most useful section for readers and it is unreconstructable afterwards.
- The connection count at the moment things fell over, and what you were doing.
- Any drift you realised the controller was **not** checking for. That list is the honest limitation section of the article.

**`docs/measurements.md`:** three tables, ten runs per timing cell, with the exact command or SQL that produced each number and the poll interval clearly labelled.

**`docs/raw/`:** `pg_stat_statements` dumps before and after each sweep, `pg_stat_activity` samples during the fan-out test, and the raw controller logs from the deletion deadlock.

---

---

### Still to write before this becomes `docs/brief.md`

The current project instructions require a **`Background — the concept explained from scratch`** section, sitting after *Maturity* and before *The system*, in eight parts: what it actually is (from first principles), the problem it solves, how it works mechanically, a vocabulary table, what to understand first, related and adjacent topics, common misconceptions with corrections, and where to read more. Only the last part exists above. The other seven are not yet written.

Write them before copying this file into the repo as `docs/brief.md`, since that file is frozen on day one and also serves the article writer and anyone arriving at the repo from the article.
