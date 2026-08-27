#!/usr/bin/env bash
# End-to-end smoke test for gotruectl against real Docker. Builds the binary,
# exercises every command (postgres, tenant, config/SMTP, status, backup,
# key/admin, update success+rollback+pull-failure, JWT rotation), and tears
# everything down afterward regardless of outcome.
#
# Requires: Docker running, network access (pulls postgres/mailpit/redis/
# supabase-auth images), a free set of ports (19999, 19998, 8025, 1025).
#
# Usage: ./scripts/smoke-test.sh
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$(mktemp -d)/gotruectl"
FAILURES=0

step() { printf '\n=== %s ===\n' "$1"; }
pass() { printf 'PASS: %s\n' "$1"; }
fail() { printf 'FAIL: %s\n' "$1"; FAILURES=$((FAILURES + 1)); }
check() { if "$@"; then pass "$*"; else fail "$*"; fi; }

# wait_healthy polls a port's /health for up to ~15s — GoTrue can take a
# couple seconds to finish its migrations after `docker run` returns, so a
# single immediate curl is a real race, not a good check.
wait_healthy() {
	local port="$1" label="$2"
	for _ in $(seq 1 15); do
		[ "$(curl -s -o /dev/null -w '%{http_code}' "http://localhost:$port/health" 2>/dev/null)" = "200" ] && { pass "$label healthy"; return 0; }
		sleep 1
	done
	fail "$label not healthy"
	return 1
}

cleanup() {
	step "cleanup"
	"$BIN" tenant delete --name kyc >/dev/null 2>&1
	"$BIN" tenant delete --name admin >/dev/null 2>&1
	"$BIN" postgres down >/dev/null 2>&1
	docker rm -f postgres mailpit gotrue-status-test >/dev/null 2>&1
	docker volume rm gotrue-postgres-data >/dev/null 2>&1
	docker network rm gotrue-net >/dev/null 2>&1
	rm -rf "$HOME/.gotrue-builder"
	rm -rf "$(dirname "$BIN")"
	echo "cleaned up"
}
trap cleanup EXIT

step "build"
if ! go build -o "$BIN" "$REPO_ROOT/cmd/gotruectl"; then
	fail "go build"
	exit 1
fi
pass "go build"
gofmt -l "$REPO_ROOT" | grep -q . && fail "gofmt (files need formatting)" || pass "gofmt"
(cd "$REPO_ROOT" && go vet ./...) && pass "go vet" || fail "go vet"

step "postgres up (fresh)"
"$BIN" postgres up || fail "postgres up"
[ "$(stat -c %a "$HOME/.gotrue-builder/postgres.env")" = "600" ] && pass "postgres.env is mode 600" || fail "postgres.env has loose permissions"

step "config set-smtp + inheritance into tenant create"
docker run -d --name mailpit --network gotrue-net -p 8025:8025 axllent/mailpit:latest >/dev/null
"$BIN" config set-smtp --host mailpit --port 1025 --user "" --pass "hunter2" \
	--admin-email noreply@abc.com.np --sender-name "ABC Portal (dev)" </dev/null
grep -q "smtp_host: mailpit" "$HOME/.gotrue-builder/config.yaml" && pass "config file has smtp_host" || fail "config file missing smtp_host"
[ "$(stat -c %a "$HOME/.gotrue-builder/config.yaml")" = "600" ] && pass "config.yaml is mode 600" || fail "config.yaml has loose permissions"

step "tenant create x2 (and: does the interactive prompt ever echo the generated JWT secret?)"
CREATE_OUT=$(mktemp)
"$BIN" tenant create --name kyc --port 19999 --signup </dev/null >"$CREATE_OUT" 2>&1
cat "$CREATE_OUT"
"$BIN" tenant create --name admin --port 19998 </dev/null
sleep 2
grep -q "GOTRUE_SMTP_HOST=mailpit" "$HOME/.gotrue-builder/tenants/kyc.env" && pass "kyc env has SMTP wired" || fail "kyc env missing SMTP"
KYC_SECRET=$(grep GOTRUE_JWT_SECRET "$HOME/.gotrue-builder/tenants/kyc.env" | cut -d= -f2)
grep -qF "$KYC_SECRET" "$CREATE_OUT" && fail "generated JWT secret was echoed to the terminal during tenant create" || pass "generated JWT secret never appears in tenant create's output"
rm -f "$CREATE_OUT"
[ "$(stat -c %a "$HOME/.gotrue-builder/tenants/kyc.env")" = "600" ] && pass "tenant env file is mode 600" || fail "tenant env file has loose permissions"
[ "$(stat -c %a "$HOME/.gotrue-builder/tenants")" = "700" ] && pass "tenants dir is mode 700" || fail "tenants dir has loose permissions"
wait_healthy 19999 kyc
wait_healthy 19998 admin

step "duplicate create is rejected"
"$BIN" tenant create --name kyc --port 20000 </dev/null 2>/dev/null && fail "duplicate create should have failed" || pass "duplicate create rejected"

step "status distinguishes managed vs unmanaged"
docker run -d --name gotrue-status-test --network gotrue-net -p 19997:9999 \
	-e GOTRUE_API_HOST=0.0.0.0 -e PORT=9999 -e GOTRUE_DB_DRIVER=postgres \
	-e GOTRUE_JWT_SECRET=x supabase/auth:v2.196.0 >/dev/null
sleep 1
STATUS_OUT="$("$BIN" status)"
echo "$STATUS_OUT" | grep -q "gotrue-status-test.*no" && pass "unmanaged container flagged" || fail "unmanaged container not flagged"
echo "$STATUS_OUT" | grep -q "gotrue-kyc.*yes" && pass "kyc flagged managed" || fail "kyc not flagged managed"
docker rm -f gotrue-status-test >/dev/null 2>&1

step "doctor: whole-system health check"
"$BIN" doctor
DOCTOR_EXIT=$?
[ "$DOCTOR_EXIT" -eq 0 ] && pass "doctor exits 0 when everything is healthy" || fail "doctor should have exited 0 (got $DOCTOR_EXIT)"
"$BIN" doctor | grep -q "postgres.*OK" && pass "doctor reports postgres OK" || fail "doctor did not report postgres OK"
"$BIN" doctor | grep -q "tenant kyc.*OK" && pass "doctor reports tenant kyc OK" || fail "doctor did not report tenant kyc OK"
"$BIN" doctor | grep -q "no backups yet" && pass "doctor warns about missing backups before any have run" || fail "doctor should warn about no backups yet"

step "backup run/list"
"$BIN" backup run --all
"$BIN" backup list | grep -q kyc && pass "backup list shows kyc" || fail "backup list missing kyc"
LATEST_KYC_BACKUP=$(ls -t "$HOME/.gotrue-builder/backups/kyc/"*.sql.gz | head -1)
[ "$(stat -c %a "$LATEST_KYC_BACKUP")" = "600" ] && pass "backup file is mode 600" || fail "backup file has loose permissions"
[ "$(stat -c %a "$HOME/.gotrue-builder/backups/kyc")" = "700" ] && pass "backup dir is mode 700" || fail "backup dir has loose permissions"
"$BIN" doctor | grep -q "kyc backup.*OK" && pass "doctor sees the fresh backup" || fail "doctor did not pick up the fresh backup"

step "key mint + admin API round trip (the abc_project_app admin-provisioning gap)"
"$BIN" admin create-user --tenant admin --email smoketest@abc.com.np --password 'Sm0keTest!23' --email-confirm >/dev/null
LOGIN_CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:19998/token?grant_type=password" \
	-H "Content-Type: application/json" -d '{"email":"smoketest@abc.com.np","password":"Sm0keTest!23"}')
[ "$LOGIN_CODE" = "200" ] && pass "admin-created user can log in" || fail "admin-created user cannot log in (got $LOGIN_CODE)"
"$BIN" backup run --tenant admin >/dev/null
LATEST=$(ls -t "$HOME/.gotrue-builder/backups/admin/"*.sql.gz | head -1)
zcat "$LATEST" | grep -q smoketest@abc.com.np && pass "backup captured the new user" || fail "backup missing the new user"

step "update run: success path (same known-good version)"
"$BIN" update run --tenant kyc --version v2.196.0 || fail "update run (success path)"
wait_healthy 19999 "kyc after update"
docker ps -a --filter name=gotrue-kyc-rollback -q | grep -q . && fail "rollback container not cleaned up" || pass "rollback container cleaned up"

step "update run: rollback path (image that won't answer /health)"
BEFORE_IMAGE=$(docker inspect -f '{{.Config.Image}}' gotrue-kyc)
"$BIN" update run --tenant kyc --image redis:alpine --version dummy --timeout 8s
[ $? -ne 0 ] && pass "update reports failure" || fail "update should have reported failure"
sleep 2
AFTER_IMAGE=$(docker inspect -f '{{.Config.Image}}' gotrue-kyc)
[ "$BEFORE_IMAGE" = "$AFTER_IMAGE" ] && pass "rolled back to original image" || fail "image changed after failed update: $AFTER_IMAGE"
wait_healthy 19999 "kyc after rollback"

step "update run: pull-failure never touches the running container"
BEFORE_ID=$(docker inspect -f '{{.Id}}' gotrue-kyc)
"$BIN" update run --tenant kyc --version does-not-exist-tag-xyz 2>/dev/null
AFTER_ID=$(docker inspect -f '{{.Id}}' gotrue-kyc)
[ "$BEFORE_ID" = "$AFTER_ID" ] && pass "container untouched on pull failure" || fail "container was touched despite pull failure"

step "tenant config (vertical, single-tenant) + tenant config set (edit + safe restart)"
"$BIN" tenant config >/dev/null 2>&1 && fail "tenant config without --name should require it" || pass "tenant config requires --name"
"$BIN" tenant config --name kyc | grep -q "API_EXTERNAL_URL" && pass "tenant config shows kyc's full settings" || fail "tenant config missing expected fields for kyc"
"$BIN" tenant config --name kyc | grep -q "GOTRUE_JWT_SECRET.*(set)" && pass "tenant config masks the JWT secret" || fail "tenant config did not mask the JWT secret"
"$BIN" tenant config --name admin | grep -q "disabled" && pass "tenant config shows admin signup disabled" || fail "tenant config wrong signup state"
"$BIN" tenant config set --name kyc --jwt-aud custom-aud --timeout 15s
grep -q "GOTRUE_JWT_AUD=custom-aud" "$HOME/.gotrue-builder/tenants/kyc.env" && pass "tenant config set applied jwt-aud" || fail "tenant config set did not apply jwt-aud"
wait_healthy 19999 "kyc after config set"
"$BIN" tenant config set --name kyc --jwt-aud custom-aud >/tmp/config-set-noop.out
grep -q "no changes" /tmp/config-set-noop.out && pass "tenant config set is a no-op when nothing changed" || fail "tenant config set should have reported no changes"
rm -f /tmp/config-set-noop.out

step "caddyfile: security-hardened reverse-proxy config generation"
CADDY_OUT=$("$BIN" caddyfile --tenant kyc --domain auth.example.com)
echo "$CADDY_OUT" | grep -q "auth.example.com {" && pass "caddyfile targets the right domain" || fail "caddyfile missing domain block"
echo "$CADDY_OUT" | grep -q "reverse_proxy 127.0.0.1:19999" && pass "caddyfile proxies to the right port" || fail "caddyfile has wrong/missing proxy target"
echo "$CADDY_OUT" | grep -q "Strict-Transport-Security" && pass "caddyfile sets HSTS" || fail "caddyfile missing security headers"
echo "$CADDY_OUT" | grep -qE "^\s*@public path .*/admin" && fail "caddyfile's allowlist must never include /admin" || pass "caddyfile allowlist excludes /admin"
echo "$CADDY_OUT" | grep -q 'respond "Not Found" 404' && pass "caddyfile default-denies everything not allowlisted" || fail "caddyfile missing default-deny catch-all"
"$BIN" caddyfile --tenant kyc --domain auth.example.com --out /tmp/gotruectl-caddytest.Caddyfile >/dev/null
[ -f /tmp/gotruectl-caddytest.Caddyfile ] && pass "caddyfile --out writes a file" || fail "caddyfile --out did not write a file"
rm -f /tmp/gotruectl-caddytest.Caddyfile
"$BIN" caddyfile --all --domain-template '{tenant}.auth.example.com' | grep -q "admin.auth.example.com {" && pass "caddyfile --all covers every tenant" || fail "caddyfile --all missing a tenant block"

step "dashboard: TUI starts, shows live data, and quits cleanly"
if command -v tmux >/dev/null 2>&1; then
	tmux kill-session -t gotruectl-dashboard-test 2>/dev/null
	tmux new-session -d -s gotruectl-dashboard-test -x 100 -y 30 "$BIN dashboard"
	sleep 3
	PANE=$(tmux capture-pane -p -t gotruectl-dashboard-test)
	echo "$PANE" | grep -qi "gotruectl dashboard" && pass "dashboard renders its title" || fail "dashboard did not render"
	echo "$PANE" | grep -q "postgres" && echo "$PANE" | grep -qi "OK" && pass "dashboard shows live postgres status" || fail "dashboard did not show postgres status"
	echo "$PANE" | grep -q "tenant kyc" && pass "dashboard shows the kyc tenant" || fail "dashboard did not show kyc"
	tmux send-keys -t gotruectl-dashboard-test "q"
	sleep 1
	tmux has-session -t gotruectl-dashboard-test 2>/dev/null && { fail "dashboard did not quit on q"; tmux kill-session -t gotruectl-dashboard-test 2>/dev/null; } || pass "dashboard quits cleanly on q"
else
	echo "tmux not available — skipping dashboard interactive check"
fi

step "update rotate-jwt-secret"
OLD_SECRET=$(grep GOTRUE_JWT_SECRET "$HOME/.gotrue-builder/tenants/kyc.env")
"$BIN" update rotate-jwt-secret --tenant kyc
NEW_SECRET=$(grep GOTRUE_JWT_SECRET "$HOME/.gotrue-builder/tenants/kyc.env")
[ "$OLD_SECRET" != "$NEW_SECRET" ] && pass "secret changed" || fail "secret unchanged"
"$BIN" admin list-users --tenant kyc >/dev/null && pass "admin API works with rotated secret" || fail "admin API failed after rotation"

step "results"
if [ "$FAILURES" -eq 0 ]; then
	echo "ALL CHECKS PASSED"
	exit 0
else
	echo "$FAILURES CHECK(S) FAILED"
	exit 1
fi
