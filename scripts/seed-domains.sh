#!/usr/bin/env bash
# Seed served domains into cloistr-email via the internal domain-admin API.
#
# Supersedes the old direct-SQL seeder: the API owns the write, validates the
# key, and (on activate) refreshes every replica's signer map via pub/sub — so
# no pod roll is needed. Idempotent: import tolerates an already-registered
# domain, and verify/activate are safe to re-run.
#
# Uses the DKIM private keys from generate-dkim-keys.sh so the signing key
# matches the DNS record you published. Per domain it does:
#   import key -> verify DNS -> activate (only once DNS is live).
#
# The API must be reachable and INTERNAL_API_SECRET set on the service. From a
# workstation, port-forward first:
#   kubectl -n cloistr port-forward svc/cloistr-email-backend 8080:8080
#
# Usage:
#   INTERNAL_API_SECRET=... ./scripts/seed-domains.sh [-u API_URL] [-k KEYS_DIR] [-s SELECTOR] domain...
#   INTERNAL_API_SECRET=... ./scripts/seed-domains.sh cloistr.xyz coldforge.xyz aegis-hq.xyz aegisitservices.com
set -euo pipefail

API_URL="${API_URL:-http://localhost:8080}"
KEYS_DIR="${KEYS_DIR:-$(cd "$(dirname "$0")/dkim-keys" && pwd)}"
SELECTOR="${DKIM_SELECTOR:-mail}"

while [[ $# -gt 0 ]]; do
    case $1 in
        -u|--url) API_URL="$2"; shift 2 ;;
        -k|--keys) KEYS_DIR="$2"; shift 2 ;;
        -s|--selector) SELECTOR="$2"; shift 2 ;;
        -h|--help) grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        -*) echo "unknown option: $1" >&2; exit 1 ;;
        *) break ;;
    esac
done

: "${INTERNAL_API_SECRET:?set INTERNAL_API_SECRET}"
[[ $# -ge 1 ]] || { echo "give at least one domain" >&2; exit 1; }
command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

AUTH=(-H "Authorization: Bearer ${INTERNAL_API_SECRET}" -H "Content-Type: application/json")
base="${API_URL%/}/internal/v1/domains"

# call METHOD URL [BODY] -> echoes HTTP status; response body left in $BODY
call() {
    local method="$1" url="$2" body="${3:-}"
    local tmp; tmp="$(mktemp)"
    local args=(-sS -o "$tmp" -w '%{http_code}' -X "$method" "${AUTH[@]}")
    [[ -n "$body" ]] && args+=(--data "$body")
    local code; code="$(curl "${args[@]}" "$url")"
    BODY="$(cat "$tmp")"; rm -f "$tmp"
    echo "$code"
}

rc=0
for domain in "$@"; do
    key_file="${KEYS_DIR}/${SELECTOR}.${domain}.private.pem"
    echo "=== ${domain} ==="
    if [[ ! -f "$key_file" ]]; then
        echo "  SKIP: no key at ${key_file}"; rc=1; continue
    fi

    # 1. Import (create): 201 created, 409 already registered — both fine.
    body="$(jq -n --arg d "$domain" --arg s "$SELECTOR" --rawfile k "$key_file" \
        '{domain:$d, selector:$s, dkim_private_key:$k}')"
    code="$(call POST "$base" "$body")"
    case "$code" in
        201) echo "  imported" ;;
        409) echo "  already registered" ;;
        *)   echo "  import FAILED ($code): $BODY"; rc=1; continue ;;
    esac

    # 2. Verify DNS — flips verified only when the DKIM TXT is published.
    code="$(call POST "${base}/${domain}/verify")"
    if [[ "$code" != "200" ]]; then
        echo "  verify FAILED ($code): $BODY"; rc=1; continue
    fi
    if [[ "$(echo "$BODY" | jq -r '.dns.dkim_present // false')" != "true" ]]; then
        echo "  NOT verified — publish the DKIM TXT then re-run. dns=$(echo "$BODY" | jq -c '.dns')"
        continue
    fi
    echo "  verified"

    # 3. Activate — idempotent; refuses if not verified. Triggers signer reload.
    code="$(call POST "${base}/${domain}/activate")"
    if [[ "$code" == "200" ]]; then
        echo "  active — signing live"
    else
        echo "  activate FAILED ($code): $BODY"; rc=1
    fi
done

exit $rc
