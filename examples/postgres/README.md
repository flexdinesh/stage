# Compose example: Stage main server on Postgres

Stage keeps its data in SQLite by default. This example runs the main server
on Postgres instead: the Stage image built from the repository `Dockerfile`
(the only build with Postgres support) and a Postgres container. It is a
starting point to adapt, not a supported deployment. Contract:
[docs/storage.md](../../docs/storage.md#engines-and-selection). Guide:
[Multiple machines](https://stage.hoplabs.dev/docs/usage/workers#postgres).

| Service | What |
| --- | --- |
| `postgres` | `postgres:19.0-alpine3.24` (the same pin as `STAGE_PG_IMAGE` in `mise.toml`), database and role `stage`, a `pg_isready` healthcheck, data in the named volume `pgdata`, no published port |
| `app` | Stage, built from `../..`, started once Postgres is healthy, `STAGE_DATABASE_URL` pointing at it, published on `127.0.0.1:7380`, healthcheck `/stage status` |

## Start

```sh
cd examples/postgres
echo 'STAGE_PG_PASSWORD=<password>' > .env
docker compose up -d --build
docker compose ps        # both services healthy
curl -s http://127.0.0.1:7380/api/health   # "engine":"postgres"
```

- The password goes into the database URL as is, so keep it URL-safe
  (letters, digits, `-`, `_`, `.`, `~`), for example `openssl rand -hex 24`.
- `.env` is also passed to Stage, so other settings such as
  `STAGE_PUBLIC_URL` go there too ([`.env.example`](../../.env.example)).
- On first start the server applies every migration to the empty database.
  The startup log names the engine (`database engine=postgres`), never the
  URL.
- The `app` service publishes the same port as the production
  [`compose.yaml`](../../compose.yaml). Run one or the other.

Stop with `docker compose down`. `docker compose down -v` also deletes the
`pgdata` volume, and with it every task and journal.

## What differs from SQLite

- **Data dir.** Stage still needs one, but on Postgres it holds only
  `stage.lock` and sockets, so the `app` service has no volume.
- **One server per database.** The server holds a Postgres advisory lock for
  its lifetime. A second server on the same database exits 75, whatever its
  data dir. Run one replica and stop the old one before starting a new one.
- **Backups are yours.** The server takes none. Before applying migrations
  after an upgrade it logs a warning that no backup was taken, then
  migrates. Back up with `pg_dump` (or your provider's snapshots), for
  example before every upgrade:

  ```sh
  docker compose exec postgres pg_dump -U stage -Fc stage > stage-$(date +%F).dump
  ```

- **Locale.** The database is initialized with Postgres's builtin `C.UTF-8`
  locale, so search is case-insensitive beyond ASCII. Your own Postgres needs
  a UTF-8 ctype for that ([docs/storage.md](../../docs/storage.md#search)).
- **Your own Postgres.** Drop the `postgres` service and set
  `STAGE_DATABASE_URL` to your server. The role needs to own the database (or
  hold `CREATE` on it) for the `pg_trgm` extension.

There is no copy from an existing SQLite `stage.db` yet: this example starts
with an empty database.

## In front of it

Stage has no user authentication. Keep the port on loopback and put an
authenticating HTTPS proxy in front, as for any main server: see
[Auth proxy](https://stage.hoplabs.dev/docs/usage/workers#auth-proxy).
