# gotrue-builder: container-orchestrator CLI — plan

## Goal

A single Go CLI (`gotruectl`, working name) that runs entirely on top of
Docker and manages the full lifecycle of:

- one shared Postgres container (holds every tenant's database)
- N GoTrue containers, one per tenant (`admin`, `kyc`, whatever you add later)

All lifecycle actions — create a tenant, delete a tenant, list tenants,
start/stop, view logs — happen through CLI subcommands. No docker-compose
file to hand-edit, no systemd units, no bare-metal Go toolchain on the host.
Docker is the only host dependency.

## Why this over what we tried earlier today

- Bare-metal (`build.sh` + systemd + manually-provisioned Postgres) works but
  means installing/updating a Go toolchain on the VM and hand-maintaining
  systemd units and env files per tenant.
- This collapses that into one binary: `gotruectl tenant create --name kyc`
  does everything (DB, role, schema, container) in one step.
- It's the same shape as `docker-compose.gotrue.yml` (already proven working
  earlier in this session — two GoTrue instances, one shared Postgres, two
  schemas) but turns "add a third tenant" from "hand-edit YAML" into a CLI
  command with its own state tracking.

## Key design decisions (made now, revisit if wrong)

1. **GoTrue image: use the official `supabase/auth:v2.196.0` image directly.**
   No build step needed for v1. `gotrue-builder`'s existing `build.go` (clone
   supabase/auth from source + `go build`) stays in this repo as an optional
   *phase 2*: swap in a self-built image only if you need a patched GoTrue or
   want to avoid Docker Hub pulls. Don't build that until v1 works.

2. **One shared Postgres container, one database per tenant.** Matches what
   `setup-db.sh` already proved works this session: separate DB + role per
   tenant (not just separate schemas) for real isolation, one Postgres
   process for low resource footprint. Named volume so data survives
   container recreation: `gotrue-postgres-data`.

3. **Docker is the source of truth — no separate state file to drift out of
   sync.** Tag every container the CLI creates with labels:
   `--label managed-by=gotruectl --label tenant=<name> --label role=gotrue`
   (and `role=postgres` for the DB container). `tenant list` queries
   `docker ps -a --filter label=managed-by=gotruectl` instead of reading a
   JSON file that could disagree with reality after a manual `docker rm`.
   The *generated* per-tenant `.env` file (see below) is the only other
   record, and it's derived from what you actually pass to `docker run`, not
   a separate blessed store.

4. **One Docker network** (`gotrue-net`) so GoTrue containers reach Postgres
   by container name (`postgres`) instead of host IP tricks. Create once,
   everything joins it.

5. **Per-tenant `.env` file on the host**, mounted/passed via
   `docker run --env-file`, written to e.g. `~/.gotrue-builder/tenants/<name>.env`
   (mode 600 — it holds the JWT secret and DB password). This is the same
   godotenv-format file GoTrue's own `-c` flag reads (verified against
   GoTrue source this session: `internal/conf/confload/confload.go` uses
   `github.com/joho/godotenv`), so if you ever go back to bare-metal for one
   tenant, the file just works there too.

## Command surface (v1)

```
gotruectl postgres up                          # start shared postgres (idempotent)
gotruectl postgres down                        # stop it (data stays in the volume)

gotruectl tenant create --name kyc [--port 9999] [--signup]
                                                # prompts for anything not passed as a flag:
                                                #   port, external URL, site URL, jwt secret
                                                #   (offer a generated default), allow signup y/n
                                                # then: ensures postgres is up, creates role+db+schema,
                                                # writes ~/.gotrue-builder/tenants/kyc.env,
                                                # docker run -d --name gotrue-kyc ... supabase/auth:v2.196.0

gotruectl tenant list                          # table: name, port, status, image, uptime
gotruectl tenant logs --name kyc [--follow]    # wraps `docker logs`
gotruectl tenant start --name kyc
gotruectl tenant stop  --name kyc
gotruectl tenant delete --name kyc [--keep-data]
                                                # docker rm -f gotrue-<name>; drops DB+role unless --keep-data
```

## `tenant create` flow, step by step

1. Check Docker is reachable (`docker info`) — fail fast with a clear error
   if not.
2. Ensure `gotrue-net` network exists (`docker network create gotrue-net`,
   ignore "already exists").
3. Ensure the `postgres` container is running (start it if not — see
   Postgres section below).
4. Collect config: `--name` is required; everything else is a flag with a
   prompt fallback (reuse the prompt/promptYesNo pattern already written for
   the abandoned bare-metal `tenant create` — same idea, just now the output
   is a `docker run` invocation instead of a systemd env file).
5. `docker exec postgres psql ...` (or connect directly via `database/sql` +
   `lib/pq`/`jackc/pgx` from the Go binary — simpler to shell out to `psql`
   inside the postgres container, avoids adding a DB driver dependency to
   this CLI) to idempotently:
   - `CREATE ROLE gotrue_<name> LOGIN PASSWORD '<generated>'`
   - `CREATE DATABASE gotrue_<name> OWNER gotrue_<name>`
   - `CREATE SCHEMA IF NOT EXISTS auth AUTHORIZATION gotrue_<name>`
   (This is exactly `setup-db.sh`'s logic, already written and verified
   working + idempotent this session — port it into Go or literally embed
   the SQL and run it via `docker exec postgres psql`.)
6. Write `~/.gotrue-builder/tenants/<name>.env` with:
   `GOTRUE_API_HOST, PORT, API_EXTERNAL_URL, GOTRUE_DB_DRIVER, DATABASE_URL`
   (pointing at `postgres:5432` — the network alias, not localhost),
   `GOTRUE_DB_NAMESPACE=auth, GOTRUE_JWT_SECRET, GOTRUE_JWT_EXP, GOTRUE_JWT_AUD,
   GOTRUE_SITE_URL, GOTRUE_DISABLE_SIGNUP`.
7. `docker run -d --name gotrue-<name> --network gotrue-net \
     --label managed-by=gotruectl --label tenant=<name> --label role=gotrue \
     -p <port>:9999 --env-file <path> --restart unless-stopped \
     supabase/auth:v2.196.0`
8. Print the container name, port, and a reminder that the Admin API
   (`/admin/*`) on that port must not be exposed publicly (same nginx-split
   concern raised earlier this session — out of scope for this CLI, but
   don't forget it before opening the port to the internet).

## Postgres container

```
docker volume create gotrue-postgres-data     # once
docker network create gotrue-net              # once
docker run -d --name postgres --network gotrue-net \
  --label managed-by=gotruectl --label role=postgres \
  -e POSTGRES_PASSWORD=<generated, stored in ~/.gotrue-builder/postgres.env> \
  -v gotrue-postgres-data:/var/lib/postgresql/data \
  postgres:15-alpine
```

`gotruectl postgres up` should be idempotent: if a container named
`postgres` with the `managed-by=gotruectl` label already exists, just
`docker start` it (or no-op if already running) instead of erroring.

## Suggested package layout

```
gotrue-builder/
  main.go            # dispatch: postgres | tenant
  docker.go           # thin wrapper around exec.Command("docker", ...) —
                       # run, inspect, list-by-label, logs, start/stop/rm
  postgres.go         # postgres up/down
  tenant.go           # tenant create/list/logs/start/stop/delete
  prompt.go           # the interactive prompt helpers (already written once
                       # this session for the bare-metal version — reusable
                       # as-is, just targets docker run instead of systemd)
  build.go            # existing "build supabase/auth from source" command —
                       # kept as the phase-2 self-built-image path, untouched
```

Keep shelling out to the real `docker` CLI (`os/exec`) rather than adding
the Docker Go SDK (`github.com/docker/docker/client`) as a dependency —
this whole tool is a thin wrapper around a handful of `docker` invocations;
the SDK is justified once you need things a CLI wrapper can't do cleanly
(streaming events, complex filters), not before.

## Open questions to settle before/while building

- **Port allocation**: auto-pick the next free port per tenant, or always
  require `--port` explicitly? (Explicit is simpler and matches "no magic"
  — recommend requiring it, at least for v1.)
- **Reverse proxy / TLS**: still needed once you expose tenants to the
  internet (nginx/Caddy in front, per-tenant subdomain → container port).
  Not this CLI's job — note it as a separate, later piece.
- **Admin ops panel** (the Go web UI over GoTrue's Admin API from earlier
  today): separate concern, works unchanged against containers since it
  only needs `base_url` + `service_role_key` per tenant — revisit only
  after tenant lifecycle is solid.
- **Backups**: `gotrue-postgres-data` volume is the only durable state
  across every tenant — worth a `gotruectl postgres backup` command
  eventually (`docker exec postgres pg_dumpall`), not required for v1.

## Suggested build order for tomorrow

1. `docker.go` — the exec.Command wrapper + a `dockerAvailable()` check.
2. `postgres up` / `postgres down`, manually verified with `docker ps`.
3. `tenant create` with **flags only** (no interactive prompts yet) against
   the official image — get one tenant fully working end-to-end (create →
   `curl` its `/health` → `docker exec` in as `gotrue_<name>` to confirm the
   DB/role/schema landed right).
4. `tenant list` / `logs` / `start` / `stop`.
5. `tenant delete` (with the `--keep-data` guard).
6. Add interactive prompts to `tenant create` (fall back to prompting for
   whatever wasn't passed as a flag).
7. Only then: revisit whether the self-built image (phase 2) is worth doing.

## Facts already verified this session (don't re-derive)

- GoTrue's `-c`/`--config` flag loads a plain `KEY=VALUE` file via
  `github.com/joho/godotenv` — the `.env` files from the bare-metal attempt
  are directly reusable as `--env-file` for `docker run`.
- GoTrue needs its target schema (`auth`) to exist *before* first run — it
  runs its own table migrations inside that schema automatically.
- Admin API routes (from `internal/api/admin.go` in supabase/auth v2.196.0):
  `GET/POST /admin/users`, `GET/PUT/DELETE /admin/users/{id}`,
  `POST /invite`, `POST /admin/generate_link` (returns `action_link`,
  does **not** send an email itself). All require
  `Authorization: Bearer <service_role JWT>` where the JWT's `role` claim
  is `service_role` (checked against `config.JWT.AdminRoles`, default
  includes `service_role`).
- `PUT /admin/users/{id}` with `{"ban_duration": "876000h"}` bans a user;
  `{"ban_duration": "none"}` unbans — there's no separate unban verb.
- Minting a `service_role` JWT locally (no third-party JWT site, never paste
  a real JWT secret into jwt.io): HS256, header
  `{"alg":"HS256","typ":"JWT"}`, payload
  `{"role":"service_role","iss":"...","iat":...,"exp":...}`, both
  base64url-encoded, signed via `openssl dgst -sha256 -hmac <secret>`,
  also base64url-encoded. A working script for this existed in the deleted
  `gotrue-deploy/scripts/gen-service-role-jwt.sh` — recreate it if needed.
- `supabase/auth` is pure Go, builds fine with `CGO_ENABLED=0` cross-compiled
  for `linux/amd64` and `linux/arm64` — relevant only if/when phase 2
  (self-built image) happens.
