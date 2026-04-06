#!/usr/bin/env bash
set -euo pipefail

# Pulls the auto-generated admin password from Continuwuity pod logs (create-user
# startup command), stores it in Kubernetes (Secret) and in KeePassXC via
# keepassxc-cli. Matches the usual pattern: read -s for the .kdbx master
# password; keepassxc-cli -q reads the database password from stdin line 1 and
# (with -p) the new entry password from line 2.
#
# The Matrix admin password is never printed.
#
# After a successful bootstrap, remove CONTINUWUITY_ADMIN_EXECUTE* from the
# StatefulSet so restarts do not re-run create-user.
#
# Requires: kubectl, keepassxc-cli
# Defaults match sax’s KeePass layout; override with env if needed.
# Env (optional):
#   NAMESPACE=matrix  POD_NAME=continuwuity-0  SECRET_NAME=continuwuity-admin
#   LOG_TAIL=8000   KEEPASS_DB  KEEPASS_ENTRY  ADMIN_LOCALPART  SERVER_NAME

NAMESPACE="${NAMESPACE:-matrix}"
POD_NAME="${POD_NAME:-continuwuity-0}"
SECRET_NAME="${SECRET_NAME:-continuwuity-admin}"
ADMIN_LOCALPART="${ADMIN_LOCALPART:-admin}"
SERVER_NAME="${SERVER_NAME:-chat.sax.lgbt}"
KEEPASS_DB="${KEEPASS_DB:-/mnt/storage/KeePassXC/Passwords.kdbx}"
KEEPASS_ENTRY="${KEEPASS_ENTRY:-Root/Infra/Continuwuity/Users/@admin:chat.sax.lgbt}"

if ! command -v kubectl >/dev/null; then
  echo "kubectl not found" >&2
  exit 1
fi
if ! command -v keepassxc-cli >/dev/null; then
  echo "keepassxc-cli not found" >&2
  exit 1
fi

LOG_TAIL="${LOG_TAIL:-8000}"
USER_ID="@${ADMIN_LOCALPART}:${SERVER_NAME}"

set +e
LOGS_CUR="$(kubectl -n "${NAMESPACE}" logs "${POD_NAME}" --tail="${LOG_TAIL}" 2>&1)"
_kubectl_ec=$?
LOGS_PREV="$(kubectl -n "${NAMESPACE}" logs "${POD_NAME}" --previous --tail="${LOG_TAIL}" 2>&1)"
_prev_ec=$?
set -e
if (( _kubectl_ec != 0 )); then
  echo "kubectl logs failed (exit ${_kubectl_ec}). Check context, namespace, and pod name." >&2
  printf '%s\n' "${LOGS_CUR}" >&2
  exit 1
fi
# --previous only exists after the continuwuity container has restarted at least once; otherwise
# kubectl returns BadRequest — ignore so we do not merge that error text into the log blob.
if (( _prev_ec != 0 )); then
  LOGS_PREV=""
fi
LOGS="${LOGS_CUR}"$'\n'"${LOGS_PREV}"

# Continuwuity often logs "Startup command #… completed:" and "Created user…" on
# separate lines; flatten so a single-line pattern matches.
LOGS_FLAT="$(printf '%s' "${LOGS}" | tr '\n\r' ' ' | tr -s ' ')"

PREFIX="$(printf 'Created user with user_id: %s and password: `' "${USER_ID}")"
MATRIX_PASS=""
case "${LOGS_FLAT}" in
  *"${PREFIX}"*)
    REST="${LOGS_FLAT#*"${PREFIX}"}"
    MATRIX_PASS="${REST%%\`*}"
    ;;
esac

if [[ -z "${MATRIX_PASS}" ]]; then
  echo "could not find create-user password for ${USER_ID} in pod logs." >&2
  echo "Use grep 'Created user' (not just Created — that also matches RocksDB migrations)." >&2
  echo "Try: kubectl -n ${NAMESPACE} logs ${POD_NAME} --tail=${LOG_TAIL} | grep -F 'Created user'" >&2
  echo "If the container has restarted, also: ... logs ${POD_NAME} --previous ... (omit if API says no previous container)." >&2
  echo "If still empty: bootstrap never succeeded, or admin already existed (no new password logged)." >&2
  exit 1
fi

read -r -s -p "KeePass master password (${KEEPASS_DB}): " KDBX_MASTER
echo "" >&2
if [[ -z "${KDBX_MASTER}" ]]; then
  echo "empty master password, aborting" >&2
  exit 1
fi

TMP="$(mktemp)"
chmod 600 "${TMP}"
cleanup() {
  if command -v shred >/dev/null 2>&1; then
    shred -u "${TMP}" 2>/dev/null || rm -f "${TMP}"
  else
    rm -f "${TMP}"
  fi
}
trap cleanup EXIT

printf '%s' "${MATRIX_PASS}" >"${TMP}"
unset MATRIX_PASS

if ! kubectl create secret generic "${SECRET_NAME}" \
  -n "${NAMESPACE}" \
  --from-file=password="${TMP}" \
  --dry-run=client -o yaml | kubectl apply -f -; then
  echo "kubectl apply for Secret/${SECRET_NAME} failed." >&2
  exit 1
fi

if ! printf '%s\n%s\n' "${KDBX_MASTER}" "$(cat "${TMP}")" | \
  keepassxc-cli edit -q -p "${KEEPASS_DB}" "${KEEPASS_ENTRY}"; then
  echo "keepassxc-cli edit failed (Secret was already updated)." >&2
  exit 1
fi

unset KDBX_MASTER

echo "Updated Secret/${SECRET_NAME} and KeePass entry \"${KEEPASS_ENTRY}\" (password not shown)." >&2
