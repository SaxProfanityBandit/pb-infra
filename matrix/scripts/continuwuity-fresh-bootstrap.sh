#!/usr/bin/env bash
set -euo pipefail

# Wipes Continuwuity PVC data and brings the StatefulSet back up so
# CONTINUWUITY_ADMIN_EXECUTE runs against an empty database and logs
# "Created user ... password: ..." again.
#
# Destructive: deletes all rooms, users, and keys on this homeserver volume.
#
# Env:
#   NAMESPACE=matrix          namespace where the StatefulSet and PVC live
#   KUBECTL=kubectl           alternate CLI (must accept same args as kubectl)
#   CONTINUUITY_WIPE_DB=YES or CONTINUWUITY_WIPE_DB=YES  skip confirmation prompt
#   SKIP_APPLY=1              do not kubectl apply -k (use Flux / manual apply after)
#
# If this fails with "StatefulSet … not found": your kube context may be the host
# cluster while Continuwuity runs inside a vcluster. Use the vcluster kubeconfig
# (e.g. vcluster connect …) or set NAMESPACE to where the STS actually is.

MATRIX_DIR="$(cd "$(dirname "$0")/.." && pwd)"
SYNC_SCRIPT="$(cd "$(dirname "$0")" && pwd)/sync-continuwuity-admin-from-logs.sh"

NAMESPACE="${NAMESPACE:-matrix}"
STS_NAME="${STS_NAME:-continuwuity}"
PVC_NAME="${PVC_NAME:-continuwuity-data}"
KUBECTL="${KUBECTL:-kubectl}"

die_no_sts() {
  echo "StatefulSet ${STS_NAME} not found in namespace ${NAMESPACE}." >&2
  echo "" >&2
  echo "API server context: $( "${KUBECTL}" config current-context 2>/dev/null || echo "(unknown)")" >&2
  echo "Command tried: ${KUBECTL} get statefulset/${STS_NAME} -n ${NAMESPACE}" >&2
  if ! "${KUBECTL}" get ns "${NAMESPACE}" >/dev/null 2>&1; then
    echo "Namespace ${NAMESPACE} is missing on this cluster (wrong kube context or vcluster not targeted)." >&2
  fi
  echo "" >&2
  echo "StatefulSets matching continuwuity (all namespaces):" >&2
  if "${KUBECTL}" get statefulset -A 2>/dev/null | grep -Fi continuwuity; then
    :
  else
    echo "(no match; first StatefulSets on cluster:)" >&2
    "${KUBECTL}" get statefulset -A 2>/dev/null | head -30 >&2 || true
  fi
  echo "" >&2
  echo "Fix: use the kubeconfig/context that talks to the cluster where Flux deployed matrix/ (often vcluster connect …, not the host context)." >&2
  echo "Then rerun, or set NAMESPACE=… and PVC_NAME=… if the app lives elsewhere." >&2
  exit 1
}

echo "This permanently deletes PVC/${PVC_NAME} in namespace ${NAMESPACE} (all Continuwuity data on that volume)." >&2
if [[ "${CONTINUUITY_WIPE_DB:-}" != "YES" && "${CONTINUWUITY_WIPE_DB:-}" != "YES" ]]; then
  read -r -p "Type YES to continue: " confirm
  if [[ "${confirm}" != "YES" ]]; then
    echo "Aborted." >&2
    exit 1
  fi
fi

if ! "${KUBECTL}" get "statefulset/${STS_NAME}" -n "${NAMESPACE}" >/dev/null 2>&1; then
  die_no_sts
fi

"${KUBECTL}" scale --replicas=0 -n "${NAMESPACE}" "statefulset/${STS_NAME}"
"${KUBECTL}" wait pod -n "${NAMESPACE}" -l "app.kubernetes.io/name=${STS_NAME}" --for=delete --timeout=300s 2>/dev/null || true

"${KUBECTL}" delete pvc "${PVC_NAME}" -n "${NAMESPACE}" --wait=true

if [[ "${SKIP_APPLY:-}" != "1" ]]; then
  "${KUBECTL}" apply -k "${MATRIX_DIR}"
else
  echo "SKIP_APPLY=1: apply matrix manifests yourself (e.g. Flux), then scale up." >&2
fi

"${KUBECTL}" scale --replicas=1 -n "${NAMESPACE}" "statefulset/${STS_NAME}"

echo "" >&2
echo "Watch for the password (not printed here):" >&2
echo "  ${KUBECTL} -n ${NAMESPACE} logs -f ${STS_NAME}-0 --tail=200 | grep -F 'Created user'" >&2
echo "Then run: ${SYNC_SCRIPT}" >&2
echo "When done, remove CONTINUWUITY_ADMIN_EXECUTE* from the StatefulSet so restarts do not re-run create-user." >&2
