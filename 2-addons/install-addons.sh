#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

CILIUM_VERSION="1.19.1"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SHOULDERS_PROFILE="${SHOULDERS_PROFILE:-medium}"
# Flux source selection. git is the default. oci reuses the same profile
# Kustomization paths but reconciles them from a pre-pushed OCI artifact
# (see `shoulders sync`), which is how local dirty-tree iteration and
# airgap installs consume the manifests without GitHub access.
SHOULDERS_FLUX_SOURCE="${SHOULDERS_FLUX_SOURCE:-git}"
SHOULDERS_OCI_URL="${SHOULDERS_OCI_URL:-}"
SHOULDERS_OCI_TAG="${SHOULDERS_OCI_TAG:-}"
SHOULDERS_OCI_INSECURE="${SHOULDERS_OCI_INSECURE:-false}"

# Cilium
helm repo add cilium https://helm.cilium.io/
helm install cilium cilium/cilium --version ${CILIUM_VERSION} \
   --namespace kube-system \
   --set kubeProxyReplacement=true \
   --set image.pullPolicy=IfNotPresent \
   --set ipam.mode=kubernetes

cilium status --wait

# This script installs FluxCD.

if ! command -v flux &> /dev/null
then
    echo "Flux CLI not found. Installing..."
    curl -s https://fluxcd.io/install.sh | sudo bash
fi

if ! flux check --pre &> /dev/null
then
    echo "Flux pre-check failed. Please check your environment."
    exit 1
fi

if ! flux get kustomization flux-system &> /dev/null
then
    echo "Installing FluxCD..."
    cd "$SCRIPT_DIR"
    flux install
    kubectl apply -k "profiles/${SHOULDERS_PROFILE}/flux"
else
    echo "FluxCD already installed. Reconciling..."
    cd "$SCRIPT_DIR"
    kubectl apply -k "profiles/${SHOULDERS_PROFILE}/flux"
    flux reconcile source git flux-system
fi

if [ "${SHOULDERS_FLUX_SOURCE}" = "oci" ]; then
    if [ -z "${SHOULDERS_OCI_URL}" ] || [ -z "${SHOULDERS_OCI_TAG}" ]; then
        echo "SHOULDERS_FLUX_SOURCE=oci requires SHOULDERS_OCI_URL and SHOULDERS_OCI_TAG to be set (push first, e.g. with 'shoulders sync')." >&2
        exit 1
    fi
    echo "Switching Flux source to OCI artifact ${SHOULDERS_OCI_URL}:${SHOULDERS_OCI_TAG}..."
    OCI_MANIFEST="$(mktemp)"
    {
        echo "apiVersion: source.toolkit.fluxcd.io/v1"
        echo "kind: OCIRepository"
        echo "metadata:"
        echo "  name: flux-system"
        echo "  namespace: flux-system"
        echo "spec:"
        echo "  interval: 1m"
        echo "  url: ${SHOULDERS_OCI_URL}"
        echo "  ref:"
        echo "    tag: ${SHOULDERS_OCI_TAG}"
        if [ "${SHOULDERS_OCI_INSECURE}" = "true" ]; then
            echo "  insecure: true"
        fi
    } > "${OCI_MANIFEST}"
    kubectl apply -f "${OCI_MANIFEST}"
    rm -f "${OCI_MANIFEST}"
    kubectl delete gitrepository flux-system -n flux-system --ignore-not-found
    # Repoint every Kustomization at the OCIRepository (paths are identical,
    # only sourceRef.kind differs).
    kubectl get kustomizations -n flux-system -o name | xargs -I{} kubectl patch {} -n flux-system --type merge -p '{"spec":{"sourceRef":{"kind":"OCIRepository"}}}'
    flux reconcile source oci flux-system
fi

echo "Waiting for Dex deployment..."
until kubectl -n dex get deploy dex >/dev/null 2>&1; do
    sleep 5
done

kubectl -n dex rollout status deploy/dex --timeout=10m

"$SCRIPT_DIR/../1-cluster/configure-apiserver-oidc.sh"
