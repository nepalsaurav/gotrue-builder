# gotrue-builder (gotruectl)

A Docker-based CLI (`gotruectl`) that manages a local [GoTrue](https://github.com/supabase/auth)
(Supabase's standalone auth server) deployment: one shared Postgres
container, plus one GoTrue container per tenant (e.g. `kyc`, `admin`). No
docker-compose file to hand-edit, no systemd units, no bare-metal Go
toolchain on the host — Docker is the only host dependency for day-to-day
use.

Built with [Cobra](https://github.com/spf13/cobra) (command tree, help,
shell completion) and [Viper](https://github.com/spf13/viper) (layered
config: flags → env vars → config file → defaults).

The repo also keeps the original `build` command — clone-and-build
`supabase/auth` from source — as an optional path for a self-built/patched
image instead of the official Docker Hub one.

## Install

```sh
go install github.com/nepalsaurav/gotrue-builder/cmd/gotruectl@latest
```

Puts `gotruectl` at `$(go env GOPATH)/bin/gotruectl` — make sure that
directory is on your `PATH`. Requires Go and Docker on the machine.

To update later, either re-run the line above, or:

```sh
gotruectl self-update                # go install .../cmd/gotruectl@latest
gotruectl self-update --version v0.2.0   # pin a specific tag
```

(Building locally instead: `go build -o gotruectl ./cmd/gotruectl` from a
checkout of this repo.)

## Quick start

```sh
gotruectl postgres up
gotruectl tenant create --name kyc --port 9999 --signup
gotruectl tenant create --name admin --port 9998
gotruectl status
```

## Configuration

Settings worth setting once live in `~/.gotrue-builder/config.yaml`
(override the path with `--config`). Precedence, highest first: **CLI
flags → `GOTRUCTL_*` env vars → config file → built-in defaults**.

```yaml
postgres_image: postgres:15-alpine
network: gotrue-net
volume: gotrue-postgres-data
default_site_url: http://localhost:5173
default_jwt_aud: authenticated
backup_dir: /home/you/.gotrue-builder/backups
smtp_host: smtp.yourprovider.com
smtp_port: "587"
smtp_user: apikey
smtp_pass: ""
smtp_admin_email: noreply@yourdomain.com
smtp_sender_name: "Your Portal"
```

- `gotruectl config show` — print the fully resolved config (secrets masked)
- `gotruectl config set-smtp [--host H] [--port P] [--user U] [--pass P] [--admin-email E] [--sender-name N]` —
  persist SMTP settings; every subsequent `tenant create` picks them up
  automatically. Any flag you don't pass is prompted for interactively,
  showing the current value so pressing enter keeps it.

Per-tenant secrets (JWT signing secret, DB password) are **not** config —
they're generated per tenant and live in that tenant's own
`~/.gotrue-builder/tenants/<name>.env`, which is also the `--env-file`
Docker starts the container with.

## Commands

### `postgres` — the shared Postgres container

```sh
gotruectl postgres up      # idempotent: creates it once, starts it if stopped
gotruectl postgres down    # stops it; data stays in the gotrue-postgres-data volume
```

### `tenant` — per-tenant GoTrue containers (gotruectl-managed only)

```sh
gotruectl tenant create --name kyc --port 9999 [--signup] \
    [--site-url URL] [--external-url URL] [--jwt-secret SECRET] [--jwt-aud AUD] \
    [--smtp-host H] [--smtp-port P] [--smtp-user U] [--smtp-pass P] \
    [--smtp-admin-email E] [--smtp-sender-name N]
gotruectl tenant list
gotruectl tenant config --name kyc
gotruectl tenant config set --name kyc [--site-url URL] [--external-url URL] \
    [--jwt-aud AUD] [--signup] [--smtp-host H] [--smtp-port P] [--smtp-user U] \
    [--smtp-pass P] [--smtp-admin-email E] [--smtp-sender-name N] [--timeout 30s]
gotruectl tenant logs --name kyc [--follow]
gotruectl tenant start --name kyc
gotruectl tenant stop  --name kyc
gotruectl tenant delete --name kyc [--keep-data]
```

`create` prompts interactively for anything not passed as a flag (port,
URLs, JWT secret/audience, signup) — SMTP is the one exception: it's
sourced from config only, with `--smtp-*` flags as a one-off per-tenant
override, so you're not asked to re-enter mail server credentials on every
`tenant create`. Every tenant gets its own Postgres role, database, and
`auth` schema — full isolation, one shared Postgres process.

Ensures Postgres is up automatically. Reminder printed on every create:
don't expose a tenant's `/admin/*` routes to the public internet.

`tenant config --name kyc` shows **that one tenant's** full GoTrue-level
configuration, vertically (one setting per row) — every key in its `.env`
file, not a curated subset, grouped by concern (network, auth, database,
mail) rather than alphabetically. `tenant list` only shows docker-level
state/status/image, not any of this. Secrets (`GOTRUE_JWT_SECRET`,
`DATABASE_URL`, `GOTRUE_SMTP_PASS`) show as `(set)` rather than their
actual value — use `gotruectl key` for the JWT, or read
`~/.gotrue-builder/tenants/<name>.env` directly if you need the real value
to configure another app (e.g. a backend that verifies GoTrue-issued
tokens needs the same `GOTRUE_JWT_SECRET`).

`tenant config set` edits one or more settings and applies them via the
same safe blue/green swap `update run` uses (same image, just the updated
env; automatic rollback if the container doesn't come back healthy). It
cannot change the host port or JWT secret — recreate the tenant for the
former, use `update rotate-jwt-secret` for the latter, since that needs its
own "tokens are now invalid" warning.

### `status` — every GoTrue container on the host, managed or not

```sh
gotruectl status
```

Unlike `tenant list` (gotruectl-managed tenants only), this scans for
*every* container running a `supabase/auth` image — so it also surfaces an
instance started some other way (e.g. a hand-written docker-compose stack)
if one happens to be running alongside, with a `MANAGED` column to tell
them apart.

### `doctor` — health check for the whole system

```sh
gotruectl doctor
```

`status` and `tenant config` show *state* (what's there, what it's set to);
`doctor` actively *probes* it: is Docker reachable, is Postgres accepting
connections, does each tenant's real `/health` endpoint respond, and how
stale is its last backup. Prints one `OK`/`WARN`/`FAIL` row per check plus
a summary line, and **exits non-zero if anything failed** — safe to use in
a cron job or CI step, not just interactively.

### `backup` — dump tenant user data

```sh
gotruectl backup run --tenant kyc
gotruectl backup run --all
gotruectl backup list [--tenant kyc]
```

`pg_dump`s the tenant's `auth` schema (users, identities, sessions,
refresh_tokens, MFA factors — everything GoTrue owns) inside the Postgres
container, gzips it, and writes it to
`<backup_dir>/<tenant>/<tenant>-<UTC timestamp>.sql.gz` (mode 600). No
restore command yet — restoring is a separate, materially riskier
operation; for now, `gunzip | docker exec -i postgres psql -U postgres -d gotrue_<name>`
does it manually.

### `update` — safely change a running tenant's image or JWT secret

```sh
gotruectl update run --tenant kyc --version v2.197.0 [--timeout 30s]
gotruectl update run --all --version v2.197.0
gotruectl update rotate-jwt-secret --tenant kyc [--secret ...] [--timeout 30s]
```

Docker only lets one container bind a host port at a time, so a truly
zero-downtime swap isn't possible without a reverse proxy in front of it
(out of scope for this tool — put Caddy, nginx, or whatever you already run
in front if you need that). Instead both subcommands do a safe blue/green
swap:

1. For `run`: pull the new image **before** touching anything running — if
   the pull fails (bad tag, network issue), the command aborts immediately
   and the old container is never stopped.
2. Rename the current container out of the way and stop it (releases the
   host port) rather than removing it yet.
3. Start the new container (new image, or same image with an updated
   `.env` for `rotate-jwt-secret`) on the same port/network/labels.
4. Poll `/health` until it succeeds or `--timeout` elapses.
   - **Success**: remove the renamed-aside old container. Done.
   - **Failure**: remove the failed new container, rename the old one back,
     restart it. The tenant ends up back on the exact previous, known-good
     container. The command exits non-zero either way it fails.

Expect a few seconds of downtime during the swap itself — never a missing
or broken container. `rotate-jwt-secret` invalidates every previously
issued access/refresh token for that tenant.

### `key` / `admin` — call a tenant's GoTrue Admin API

```sh
gotruectl key --tenant kyc [--ttl 1h]                 # print a raw service_role JWT
gotruectl admin create-user --tenant admin --email staff@yourdomain.com \
    [--password ...] [--email-confirm=true]
gotruectl admin list-users --tenant admin [--page 1] [--per-page 50]
```

`admin` mints a short-lived `service_role` JWT (HS256, signed with that
tenant's own `GOTRUE_JWT_SECRET` — nothing extra to configure) and calls
`http://localhost:<tenant's port>/admin/*` with it, once per invocation; the
token is never written to disk. `key` exposes the same minting for calling
the Admin API yourself with curl or another tool.

This is exactly what you need to provision an admin/staff account on a
tenant with public signup disabled: `admin create-user` creates the actual
GoTrue identity, not just a local database row.

### `build` — phase-2: build supabase/auth from source

```sh
gotruectl build [--version v2.196.0] [--dest /opt/gotrue] [--workdir DIR]
```

Clones the pinned tag of `supabase/auth`, builds it, and copies the binary
to `<dest>/gotrue-auth`. Not used by `postgres`/`tenant` (which always pull
the official image) — this is only for when you need a patched GoTrue or an
offline build. `update run --image <your-built-image>` is how you'd deploy
that build's image to a tenant afterward.

## Security

**Is GoTrue itself secure?** GoTrue (`supabase/auth`) is mature, actively
maintained, and the same code Supabase runs in production: bcrypt password
hashing, HS256-signed JWTs, TOTP MFA, configurable rate limiting. Its
security in *this* deployment depends on how it's operated, which is where
the real risk lives:

- **This tool never sets up TLS.** Every tenant defaults to plain
  `http://localhost:<port>`. Credentials, tokens, and OTP codes all travel
  in clear text until you put a TLS-terminating reverse proxy (Caddy,
  nginx, whatever you already run) in front — **do this before exposing
  any tenant port beyond localhost.** Not this tool's job; see `PLAN.md`.
- **Never expose a tenant's `/admin/*` routes publicly** — printed as a
  reminder on every `tenant create`. Anyone who can reach them with a valid
  `service_role` token (see `key`/`admin`) can create, list, and ban users.
- **Backups are not just user data — they contain live-usable tokens.**
  `confirmation_token`, `recovery_token`, and `email_change_token_new` in
  the dumped `auth.users` table can complete a password-reset or email
  takeover if they're still unexpired when someone gets hold of a backup
  file. Treat `backup_dir` as being as sensitive as the database itself
  (it's already mode 700/600 — see below — but that's not encryption).
- **JWT secrets are single points of failure per tenant.** A leaked
  `GOTRUE_JWT_SECRET` lets an attacker mint their own `service_role` token
  for that tenant. Rotate it with `update rotate-jwt-secret` if you suspect
  exposure — it invalidates every existing token for that tenant.

**What this tool does to avoid leaking secrets itself** (all verified by
`scripts/smoke-test.sh`, not just asserted):

- Every file holding a secret (`postgres.env`, `config.yaml`, tenant
  `.env` files, backup dumps) is written mode `600`; their containing
  directories are `700`.
- Interactive prompts never echo a generated secret back to the screen —
  `tenant create`'s JWT-secret prompt shows `[generated, hidden]`, not the
  actual value, so it can't end up in terminal scrollback, tmux history,
  or a recorded session.
- Secrets are never passed as plaintext `docker run`/`docker exec`
  command-line arguments, which any local user can read via `ps aux` or
  `/proc/<pid>/cmdline` for as long as that process runs. `POSTGRES_PASSWORD`
  is handed to `docker run` via a bare `-e POSTGRES_PASSWORD` (Docker reads
  it from this program's own environment instead); `CREATE ROLE ...
  PASSWORD` is sent to `psql` over stdin instead of as a `-c` argument.
- `key`'s minted token and `admin`'s JSON output are deliberately left
  **uncolored** even though everything else uses styled output — an
  embedded ANSI escape code would corrupt a captured token or piped JSON.

**What this tool can't fix, because it's inherent to Docker**: any secret
passed via `-e` or `--env-file` ends up in that container's stored config,
readable by anyone who can run `docker inspect` on the host. The trust
boundary is "who has Docker access on this machine," same as any other
Docker Compose or `docker run`-based deployment — not something a CLI
wrapper can change without moving to a different secrets mechanism
entirely (Docker secrets requires Swarm mode, which this tool doesn't use).

## Non-goals

- **Backup restore** — riskier than backup itself; do it manually for now.
- **Encrypting backups at rest** — not implemented; the backup directory
  holds plaintext SQL dumps of user tables.
- **A reverse proxy / TLS** — bring your own (Caddy, nginx, ...) in front of
  the tenant ports once you expose them to the internet.
- **A background scheduler/daemon** — `backup`, `update`, and `status` are
  all on-demand commands; wire your own cron/systemd timer around them if
  you want them automatic.
- **Auto-checking for the latest GoTrue version** — `update run --version`
  is always explicit; no network call to GitHub releases.
