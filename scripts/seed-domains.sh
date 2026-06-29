#!/usr/bin/env bash
# Seed the served-domains table in the cloistr_email database with per-domain
# DKIM keys (multi-domain). Idempotent (ON CONFLICT upsert) and reproducible.
#
# This is the policy-clean alternative to embedding DKIM private keys in the
# Atlas role: the keys live next to this script (scripts/dkim-keys/, referenced
# by path), and the DB password is sourced from the live k8s secret inside a
# one-off pod (never printed). Run after the cloistr_email DB + schema exist.
#
# After a successful seed, restart the backend so it loads the signers:
#   kubectl --context "$CTX" -n "$NS" rollout restart deploy/cloistr-email-backend
#
# Usage:  ./scripts/seed-domains.sh
# Env:    CTX (kube context, default atlantis), NS (namespace, default cloistr)
#         DOMAINS (space-separated, default the three served domains)
set -euo pipefail

CTX="${CTX:-atlantis}"
NS="${NS:-cloistr}"
KEYS="$(cd "$(dirname "$0")/dkim-keys" && pwd)"
read -r -a DOMAINS <<< "${DOMAINS:-cloistr.xyz aegis-hq.xyz aegisitservices.com}"

# Build the idempotent seed SQL (dollar-quoted PEM => no shell/SQL escaping issues).
SQL=$'\\pset pager off\n'
for d in "${DOMAINS[@]}"; do
  key_file="$KEYS/mail.$d.private.pem"
  [ -f "$key_file" ] || { echo "missing DKIM key: $key_file" >&2; exit 1; }
  pem="$(cat "$key_file")"
  SQL+="INSERT INTO domains (domain, dkim_selector, dkim_private_key, verified, active)
VALUES ('$d', 'mail', \$pem\$$pem\$pem\$, true, true)
ON CONFLICT (domain) DO UPDATE SET dkim_selector=EXCLUDED.dkim_selector,
  dkim_private_key=EXCLUDED.dkim_private_key, verified=true, active=true;
"
done
SQL+=$'SELECT \'active domains: \'||string_agg(domain, \', \') FROM domains WHERE active;\n'

# One-off pod: password from the cloistr-email-secrets/POSTGRES_PASSWORD secret,
# connect to cloistr_email through pgbouncer (postgres-rw VIP). PodSecurity-restricted.
OVERRIDES=$(cat <<'JSON'
{"spec":{"securityContext":{"runAsNonRoot":true,"runAsUser":1000,"seccompProfile":{"type":"RuntimeDefault"}},
"containers":[{"name":"c","image":"postgres:16-alpine","stdin":true,"stdinOnce":true,
"command":["sh","-c","PGPASSWORD=\"$POSTGRES_PASSWORD\" psql \"host=postgres-rw.db.aegis-hq.xyz port=6432 user=cloistr dbname=cloistr_email sslmode=require\" -tAf -"],
"env":[{"name":"POSTGRES_PASSWORD","valueFrom":{"secretKeyRef":{"name":"cloistr-email-secrets","key":"POSTGRES_PASSWORD"}}}],
"securityContext":{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]},"runAsNonRoot":true,"seccompProfile":{"type":"RuntimeDefault"}}}]}}
JSON
)

printf '%s\n' "$SQL" | kubectl --context "$CTX" -n "$NS" run "cf-email-seed-$$" \
  --rm -i --restart=Never --image=postgres:16-alpine --overrides="$OVERRIDES"
