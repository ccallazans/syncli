#!/usr/bin/env bash
# metadata.sh – captura metadados de reprodutibilidade.
# Uso: bash common/metadata.sh > results/metadata.json
set -euo pipefail

REPO_ROOT="$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel 2>/dev/null || echo '')"

GIT_COMMIT=""
if [[ -n "$REPO_ROOT" ]]; then
    GIT_COMMIT="$(git -C "$REPO_ROOT" rev-parse HEAD 2>/dev/null || echo 'desconhecido')"
fi

GO_VERSION="$(docker run --rm syncli-experiments-node sh -c 'go version 2>/dev/null || echo indisponivel' 2>/dev/null || echo 'indisponivel')"
DOCKER_VERSION="$(docker --version 2>/dev/null || echo 'indisponivel')"
KERNEL="$(uname -r 2>/dev/null || echo 'indisponivel')"
DATE_ISO="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

# Descobre a sub-rede da bridge do experimento (se já criada)
BRIDGE_SUBNET=""
if [[ "${1:-}" != "" ]]; then
    BRIDGE_SUBNET="$(docker network inspect "$1" --format '{{range .IPAM.Config}}{{.Subnet}}{{end}}' 2>/dev/null || echo '')"
fi

printf '{\n'
printf '  "git_commit": "%s",\n' "$GIT_COMMIT"
printf '  "docker_version": "%s",\n' "$DOCKER_VERSION"
printf '  "kernel": "%s",\n' "$KERNEL"
printf '  "bridge_subnet": "%s",\n' "$BRIDGE_SUBNET"
printf '  "date": "%s"\n' "$DATE_ISO"
printf '}\n'
