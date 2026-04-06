#!/usr/bin/env bash
set -euo pipefail

# Wipes Continuwuity PVC data and brings the StatefulSet back up so
# CONTINUWUITY_ADMIN_EXECUTE runs against an empty database and logs
# "Created user ... password: ..." again.
#
# Destructive: deletes all rooms, users, and keys on this homeserver volume.
#
# Env:
#   NAMESPACE=matrix
#   CONTINUUITY_WIPE_DB=YES   skip interactive confirmation
#   SKIP_APPLY=1              do not kubectl apply -k (use Flux / manual apply after)

NAMESPACE="${NAMESPACE:-matrix}"
STS_NAME="${STS_NAME:-continuwuity}"
PVC_NAME="${PVC_NAME:-continuwuity-data}"
MATRIX_DIR="$(cd "$(dirname "$0")/.." && pwd)"

echo "This permanently deletes PVC/${PVC_NAME} in namespace ${NAMESPACE} (all Continuwuity data on that volume)." >&2
if [[ "${CONTINUUITY_WIPE_DB:-}" != "YES" ]]; then
  read -r -p "Type YES to continue: " confirm
  if [[ "${confirm}" != "YES" ]]; then
    echo "Aborted." >&2
    exit 1
  fi
fi

if ! kubectl get "sts/${STS_NAME}" -n "${NAMESPACE}" >/dev/null 2>&1; then
  echo "StatefulSet ${STS_NAME} not found in ${NAMESPACE}." >&2
  exit 1
fi

kubectl scale "sts/${STS_NAME}" -n "${NAMESPACE}" --replicas=0
kubectl wait pod -n "${NAMESPACE}" -l "app.kubernetes.io/name=${STS_NAME}" --for=delete --timeout=300s 2>/dev/null || true

kubectl delete pvc "${PVC_NAME}" -n "${NAMESPACE}" --wait=true

if [[ "${SKIP_APPLY:-}" != "1" ]]; then
  kubectl apply -k "${MATRIX_DIR}"
else
  echo "SKIP_APPLY=1: apply matrix manifests yourself (e.g. Flux), then scale up." >&2
fi

kubectl scale "sts/${STS_NAME}" -n "${NAMESPACE}" --replicas=1

echo "" >&2
echo "Watch for the password (not printed here):" >&2
echo "  kubectl -n ${NAMESPACE} logs -f ${STS_NAME}-0 --tail=200 | grep -F 'Created user'" >&2
echo "Then run: ${MATRIX_DIR}/scripts/sync-continuwuity-admin-from-logs.sh" >&2
echo "When done, remove CONTINUWUITY_ADMIN_EXECUTE* from the StatefulSet so restarts do not re-run create-user." >&2
