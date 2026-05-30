#!/usr/bin/env bash
# run.sh – EXP02: Latência de detecção (fsnotify vs debounce)
# Mede o intervalo entre fs_event e debounce_out para arquivos de 1 KB,
# mostrando que o fsnotify bruto é desprezível e o debounce (200 ms) domina.
set -euo pipefail
cd "$(dirname "$0")"

source ../common/helpers.sh

# --------------------------------------------------------------------------
# Configuração
# --------------------------------------------------------------------------
REPO_ROOT="$(git rev-parse --show-toplevel)"
RESULTS_DIR="$(pwd)/results"
REPS="${REPS:-10}"
FILE_SIZE=1024  # 1 KB fixo

# Intervalo entre repetições (ms) para deixar exclusion TTL expirar e
# o fsnotify se resetar antes da próxima escrita.
INTER_REP_SLEEP=0.8

# --------------------------------------------------------------------------
# Cleanup garantido mesmo em caso de erro
# --------------------------------------------------------------------------
cleanup() {
    info "Encerrando containers..."
    docker compose down -v 2>/dev/null || true
}
trap cleanup EXIT

# --------------------------------------------------------------------------
# Preparação de diretórios e logs
# --------------------------------------------------------------------------
mkdir -p "$RESULTS_DIR/sync_A" "$RESULTS_DIR/sync_B"
rm -f "$RESULTS_DIR/nodeA.jsonl" "$RESULTS_DIR/nodeB.jsonl"
rm -f "$RESULTS_DIR/sync_A/"* "$RESULTS_DIR/sync_B/"* 2>/dev/null || true

# --------------------------------------------------------------------------
# Captura de metadados
# --------------------------------------------------------------------------
info "Capturando metadados..."
bash ../common/metadata.sh syncli-exp02_expnet > "$RESULTS_DIR/metadata.json" 2>/dev/null || \
    bash ../common/metadata.sh > "$RESULTS_DIR/metadata.json" || true

# --------------------------------------------------------------------------
# Build das imagens
# --------------------------------------------------------------------------
build_images "$REPO_ROOT"

# --------------------------------------------------------------------------
# Subir containers
# --------------------------------------------------------------------------
info "Iniciando containers..."
docker compose up -d

# Aguarda logs existirem
sleep 1

wait_peers_ready "$RESULTS_DIR/nodeA.jsonl" "$RESULTS_DIR/nodeB.jsonl" 60

# --------------------------------------------------------------------------
# Loop de experimentos
# --------------------------------------------------------------------------
info "Iniciando ${REPS} repetições (arquivo fixo 1 KB)..."

for rep in $(seq 1 "$REPS"); do
    path_name="test_$(printf '%03d' "$rep").bin"

    # Registra linha atual antes de criar o arquivo
    start_line=$(current_line "$RESULTS_DIR/nodeA.jsonl")

    # Cria arquivo no container nodeA (1 KB de zeros)
    docker exec syncli-exp02-nodeA-1 sh -c \
        "dd if=/dev/zero of=/sync/${path_name} bs=${FILE_SIZE} count=1 2>/dev/null"

    # Aguarda debounce_out para esse path em nodeA
    wait_event "debounce_out" "$RESULTS_DIR/nodeA.jsonl" "$start_line" \
        "${path_name}" 30 > /dev/null

    info "  rep ${rep}/${REPS} concluída"

    # Pausa entre repetições para estabilizar fsnotify e TTL de exclusão
    if [[ "$rep" -lt "$REPS" ]]; then
        sleep "$INTER_REP_SLEEP"
    fi
done

# --------------------------------------------------------------------------
# Coleta e geração de gráficos
# --------------------------------------------------------------------------
docker compose down -v
trap - EXIT  # Remove trap para evitar chamada dupla

run_plot "$(pwd)" "$RESULTS_DIR"

info "EXP02 concluído. Resultados em: $RESULTS_DIR/"
