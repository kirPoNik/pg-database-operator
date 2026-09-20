# pg-database-operator

A Kubernetes controller that reconciles a `PgDatabase` custom resource against
a real Postgres server. Postgres has no watch API, so the controller has to
poll — this repo is a lab measuring what that actually costs. Background,
design, and the full experiment plan: [`docs/brief.md`](docs/brief.md).

**Status: Phase 1 (baseline running).** The `PgDatabase` CRD is scaffolded
and the controller connects to Postgres and logs its version on startup.
There is no reconcile logic yet — see [`docs/build-log.md`](docs/build-log.md)
for exactly what exists and what doesn't.

## Prerequisites

`go`, `docker`, `kind`, `kubectl`, `kubebuilder` — versions actually used are
pinned in [`docs/environment.md`](docs/environment.md).

## Quickstart

```
make setup   # create the kind cluster, deploy Postgres, install the CRD
make run     # run the controller from your host; it logs the Postgres version and exits into its watch loop
```

Expected output from `make run` includes a line like:

```
INFO  setup  Connected to Postgres  {"host": "localhost", "port": "55432", "version": "PostgreSQL 16.4 ..."}
```

Stop it with Ctrl-C. Tear everything down with:

```
make teardown
```

### If `make setup` hangs on `ImagePullBackOff`

Some networks (corporate TLS interception is the known case — see
`docs/build-log.md`, 2026-09-20 entry) let the host `docker` pull images
fine but the kind node's containerd can't verify the registry's certificate.
Work around it by pulling on the host and importing directly into the node:

```
docker pull postgres:16.4
docker save postgres:16.4 -o /tmp/postgres-16.4.tar
docker exec -i pg-lab-control-plane ctr --namespace=k8s.io images import --digests --snapshotter=overlayfs - < /tmp/postgres-16.4.tar
kubectl -n postgres delete pod postgres-0 --wait=false
```

## How to verify it's actually working

```
kubectl -n postgres get pods,svc                 # postgres-0 should be Running, 1/1
kubectl -n postgres exec statefulset/postgres -- psql -U postgres -c 'SELECT version();'
```

## Reproduce the experiment

```
make experiment
```

Not implemented yet — this is Phase 3. See the experiment plan in
[`docs/brief.md`](docs/brief.md#the-experiment).

## Write-up

Article: (link once published)
Build notes, measurements, environment, and diagrams: [`docs/`](docs/)
