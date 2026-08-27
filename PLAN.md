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

---

## v1 status: done

`docker.go`, `postgres up/down`, and the full `tenant create/list/logs/
start/stop/delete` lifecycle from the build order above were implemented
and verified end-to-end against real Docker (two concurrent tenants,
independent schemas/signup settings, idempotent role/db provisioning,
`--keep-data` delete+recreate). One real bug was caught and fixed during
that testing: recreating a tenant after `--keep-data` generated a fresh DB
password for the new env file without updating the actual Postgres role,
causing GoTrue to fail SASL auth — fixed by re-syncing the role's password
via `ALTER ROLE` on every `tenant create`, not just on first creation.

## v2: Cobra/Viper rewrite + status/backup/update/admin — done

Built directly on v1's business logic (`postgresUp`, `ensureTenantDB`,
etc.) — only the argument-parsing/dispatch layer changed. Every item below
is implemented, tested against real Docker, and documented in `README.md`
(the command reference there is more current than this section — treat
this as a changelog, README as the source of truth).

- **CLI framework**: rewritten onto [Cobra](https://github.com/spf13/cobra)
  (was: stdlib `flag` + a hand-rolled `switch` in `main.go`). Repo
  restructured to the standard `cmd/<binary>/main.go` +
  `internal/<pkg>/*.go` layout specifically so `go install
  github.com/nepalsaurav/gotrue-builder/cmd/gotruectl@latest` installs a
  binary correctly named `gotruectl` (previously the module's bare name).
  `gotruectl self-update [--version TAG]` re-runs that install command for
  convenience.
- **Layered config**: [Viper](https://github.com/spf13/viper), precedence
  flags → `GOTRUCTL_*` env → `~/.gotrue-builder/config.yaml` (path
  overridable via `--config`) → defaults. Resolved the "port allocation"
  question above as planned: `--port` stays explicit, no auto-pick.
  `gotruectl config show` / `config set-smtp` manage it.
- **SMTP config**: `smtp_*` config keys + `config set-smtp`, consumed by
  `tenant create` (with `--smtp-*` flags as a per-tenant override) to wire
  `GOTRUE_SMTP_*`/`GOTRUE_EXTERNAL_EMAIL_ENABLED` into the tenant's env
  file — not interactively prompted per tenant (unlike the other create
  options) since re-entering mail credentials on every tenant would be
  tedious; it's a set-once config concern instead.
- **`status`**: resolves the "admin ops panel" open question's prerequisite
  differently than sketched — instead of a separate web UI, `status` lists
  every container running a `supabase/auth` image on the host (not just
  `managed-by=gotruectl` ones), with a `MANAGED` column, so it also
  surfaces instances started some other way.
- **`backup run`/`backup list`**: resolves the "Backups" open question
  above — implemented as `pg_dump --schema=auth` per tenant (not
  `pg_dumpall`, so it's just the GoTrue-owned data, not all of Postgres),
  gzipped, timestamped, under `backup_dir` from config. No restore command
  (noted as a non-goal — restoring is materially riskier and wasn't asked
  for).
- **`update run`**: blue/green swap for changing a tenant's GoTrue
  version — rename+stop the old container, pull-then-start the new one,
  health-check with a timeout, automatic rollback (restore the exact
  previous container) on any failure. `--image` overrides the constructed
  `supabase/auth:<version>` entirely, which is both the phase-2
  self-built-image deployment path and how the rollback path itself gets
  tested (point it at a non-GoTrue image that won't answer `/health`).
- **`update rotate-jwt-secret`**: reuses the exact same swap/rollback
  mechanism as `update run` (shared `swapContainer` function) to safely
  change a running tenant's `GOTRUE_JWT_SECRET` in place.
- **`key` / `admin create-user` / `admin list-users`**: implements the
  "Minting a service_role JWT" fact above using
  `github.com/golang-jwt/jwt/v5` (not hand-rolled HMAC/base64url) — reads
  the target tenant's own `GOTRUE_JWT_SECRET` back out of its `.env` file,
  mints a short-lived `service_role` token, and calls that tenant's
  `/admin/*` API. `admin create-user` is exactly the missing piece found in
  `abc_project_app`'s own admin-user endpoint: that endpoint creates a
  local database row but never a matching GoTrue identity, so an
  admin-provisioned account has no way to actually log in.

Deliberately **not** done, decided explicitly when asked: a background
daemon for `status`, a built-in scheduler for `backup`, or switching from
shelling out to the `docker` CLI to the Docker Go SDK
(`github.com/docker/docker/client`) — all three were raised mid-session and
declined in favor of what's above, for the same "thin wrapper, no
speculative infra" reasoning as the original phase-2/SDK calls earlier in
this document.

## v2.1: tenant config, security hardening, colorful UI, standalone docs — done

- **`tenant config` / `tenant config set`**: a table of each tenant's
  actual GoTrue-level settings (port, URLs, JWT audience, signup, SMTP
  host) read from its `.env` file — `tenant list` only shows docker-level
  state. `config set` edits one or more settings and applies them via the
  same `swapContainer` blue/green mechanism `update run` uses, generalized
  into a shared `applyEnvChangesAndRestart` helper that `update
  rotate-jwt-secret` was refactored to use too, rather than duplicating the
  rename/stop/run/health-check/rollback sequence a third time.
- **Security audit and fixes**, prompted by an explicit "make sure nothing
  leaks" ask — three real issues found and fixed, all now covered by
  `scripts/smoke-test.sh` assertions (file permission checks, and a check
  that the generated JWT secret never appears in captured command output):
  1. `postgres up`'s `docker run -e POSTGRES_PASSWORD=<value>` put the
     plaintext password in argv (readable via `ps aux`/`/proc/<pid>/cmdline`
     for any local user during the command's run) — switched to a bare
     `-e POSTGRES_PASSWORD` plus the value in the subprocess's own
     environment (`runInheritWithSecretEnv`).
  2. `ensureTenantDB`'s `CREATE ROLE ... PASSWORD '...'`/`ALTER ROLE`
     had the same argv exposure via `psql -c` — switched to feeding the SQL
     over stdin (`dockerExecInheritStdin`).
  3. `tenant create`'s interactive JWT-secret prompt echoed the actual
     generated secret as its bracketed default, visible in terminal
     scrollback/session recordings — replaced with `promptSecret`, which
     shows `[generated, hidden]` and never prints the real value.
  Documented as a full "Security" section in `README.md`: TLS is not this
  tool's job (mandatory reverse proxy before exposing a tenant port),
  backups contain live-usable tokens (not just PII), and the Docker-level
  exposure (`docker inspect` reveals any env-based secret to anyone with
  Docker access on the host) is inherent to Docker and not fixable by a
  CLI wrapper without moving to Docker secrets (Swarm-only, out of scope).
- **Colorful output**: adopted
  [lipgloss](https://github.com/charmbracelet/lipgloss) (Charm; the
  standard choice for styled terminal Go output) for bordered tables
  (`tenant list`, `tenant config`, `status`, `backup list`, `config show`)
  and green/amber/gray success/warning/muted messages. Deliberately
  **excluded** from `key`'s token output and `admin`'s JSON — both are
  meant to be piped or captured, and an embedded ANSI code would corrupt
  that. lipgloss auto-detects non-TTY/`NO_COLOR` and degrades to plain text
  on its own, verified by comparing piped vs. pty output.
- **`gotruectl --version`** now reads the real module version from Go's own
  `runtime/debug.ReadBuildInfo()` instead of a hardcoded `"dev"` — works
  automatically for any `go install .../cmd/gotruectl@<version>` with no
  `-ldflags` needed.
- **Standalone documentation**: added `CLAUDE.md` (repo orientation for a
  fresh session — layout, non-negotiable decisions, how to verify a
  change) and two skills: `smoke-test` (runs the regression suite,
  explains how to tell a real failure from test flakiness) and `release`
  (tag-and-push process, including the default-branch pitfall below).
- **Real install bug hit and fixed**: `go install .../cmd/gotruectl@latest`
  failed, resolving to a pseudo-version from a commit that predated the
  entire v1 implementation. Root cause: the GitHub repo's default branch
  was still `master` (renaming the local branch to `main` doesn't change
  that remote setting), frozen at an old commit, and Go's `@latest`
  falls back to the remote's reported default branch whenever no version
  tag exists. Fixed by setting `main` as the actual default branch
  (`gh repo edit --default-branch main`) *and*, more durably, tagging a
  real release (`v0.1.0`) — a semver tag makes `@latest` resolution
  correct regardless of default-branch state, which the `release` skill
  now encodes as the required step, not an optional nicety.
