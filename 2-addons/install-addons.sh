#!/bin/bash

set -o errexit

CILIUM_VERSION="1.19.1"

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
    cd "$(dirname "$0")"
    flux install
    kubectl apply -f flux/
else
    echo "FluxCD already installed. Reconciling..."
    flux reconcile kustomization flux-system --with-source
fi
