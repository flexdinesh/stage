# Kubernetes example: Stage main server

A minimal example for running the Stage main server in a cluster, with
workers on laptops dialing in. It is a starting point to adapt, not a
supported chart. Guide: [Main server and workers](https://stage.hoplabs.dev/docs/deploy#main-server-and-workers).
Contract: [docs/runtime.md](../../docs/runtime.md#remote-main-server-and-workers).

| File | What |
| --- | --- |
| `pvc.yaml` | `stage-data`, 1Gi ReadWriteOnce, for `stage.db`, its backups and `stage.lock` |
| `deployment.yaml` | `stage`, one replica, `Recreate`, probes on `/api/health`, the volume at `/data` |
| `service.yaml` | `stage`, ClusterIP on port 7380, for your proxy to reach |

## Image

There is no published image. Build one from the repository `Dockerfile` at the
release your workers run, and push it to your registry:

```sh
git checkout v<version>
docker build -t registry.example.com/stage:<version> .
docker push registry.example.com/stage:<version>
```

Workers and the main server must speak the same API version, so update them
together. The image reports build version `dev`, so Settings > Machines flags
every worker's build as different from the server's; that note is harmless.

The image runs as distroless `nonroot` (65532) with
`STAGE_MACHINE_PROFILE=container`: the main server's own machine never runs
local-process harnesses. Connected harnesses (T3, OpenCode) on workers do the
work.

## Apply

Set `image` and `STAGE_PUBLIC_URL` in `deployment.yaml`, then:

```sh
kubectl apply -f pvc.yaml -f service.yaml -f deployment.yaml
```

## Single replica on a local volume

- One server owns one data dir: it holds a `flock` on `stage.lock` for its
  lifetime, and SQLite needs one writer on a local filesystem. Keep
  `replicas: 1` and `strategy: Recreate`, and do not autoscale it.
- Use a block-backed storage class (cloud disk, local-path, Longhorn, Ceph
  RBD), never NFS or another network filesystem: `flock` and SQLite locking are
  unreliable there.
- Back up the volume. Before migrating, the server copies `stage.db` to
  `/data/backups/` (newest 3 kept).
- `terminationGracePeriodSeconds: 120` covers the server's 100s drain.

## In front of it

Stage has no user authentication. Keep the Service ClusterIP and put an
authenticating HTTPS proxy (or ingress) in front, at `STAGE_PUBLIC_URL`:

- User auth on everything except `/api/worker/*`, which passes through with
  its `Authorization` header unchanged (workers authenticate with machine
  tokens and pairing codes there).
- Keep the request's Host (or rewrite it to an IP literal or a host in
  `STAGE_ALLOWED_ORIGINS`): workers send no `Origin`.
- Do not buffer responses (`GET /api/worker/channel` and
  `/api/notifications/stream` are long-lived SSE streams with a keepalive
  every 15s), and allow request bodies of at least 32 MiB.

Then pair workers from Settings > Machines > Add and run
`stage worker join <link>` on each laptop.
