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

### `status` — every GoTrue container on the host, managed or not

```sh
gotruectl status
```

Unlike `tenant list` (gotruectl-managed tenants only), this scans for
*every* container running a `supabase/auth` image — so it also surfaces an
instance started some other way (e.g. a hand-written docker-compose stack)
if one happens to be running alongside, with a `MANAGED` column to tell
them apart.

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
