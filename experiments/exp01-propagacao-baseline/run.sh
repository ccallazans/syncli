#!/usr/bin/env bash
# run.sh – EXP01: Propagação ponta-a-ponta (2 nós)
# Mede o tempo total e por etapa entre modificação em nodeA e ack_recv em nodeA.
set -euo pipefail
cd "$(dirname "$0")"

source ../common/helpers.sh

# --------------------------------------------------------------------------
# Configuração
# --------------------------------------------------------------------------
REPO_ROOT="$(git rev-parse --show-toplevel)"
RESULTS_DIR="$(pwd)/results"
REPS="${REPS:-10}"
REPS_LARGE="${REPS_LARGE:-5}"

SIZES=(1048576 10485760 104857600 209715200)

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
bash ../common/metadata.sh syncli-exp01_expnet > "$RESULTS_DIR/metadata.json" 2>/dev/null || \
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
for size in "${SIZES[@]}"; do
    # if [[ "$size" -ge 154857600 ]]; then
    #     REPS_CUR="$REPS_LARGE"
    # else
        
    # fi
    REPS_CUR="$REPS"

    size_label=""
    case "$size" in
        1048576)    size_label="1 MB"   ;;
        10485760)   size_label="10 MB"  ;;
        104857600)  size_label="100 MB" ;;
        209715200)  size_label="200 MB" ;;
        *)          size_label="${size} B" ;;
    esac

    info "Tamanho: $size_label | Repetições: $REPS_CUR"

    for rep in $(seq 1 "$REPS_CUR"); do
        path_name="test_${size}_$(printf '%03d' "$rep").bin"

        # Registra linha atual antes de criar o arquivo
        start_line=$(current_line "$RESULTS_DIR/nodeA.jsonl")

        # Cria arquivo no container nodeA
        docker exec syncli-exp01-nodeA-1 sh -c \
            "dd if=/dev/zero of=/sync/${path_name} bs=${size} count=1 2>/dev/null"

        # Aguarda ack_recv para esse path em nodeA
        wait_event "ack_recv" "$RESULTS_DIR/nodeA.jsonl" "$start_line" \
            "${path_name}" 120 > /dev/null

        info "  [${size_label}] rep ${rep}/${REPS_CUR} concluída"
    done
done

# --------------------------------------------------------------------------
# Coleta e geração de gráficos
# --------------------------------------------------------------------------
docker compose down -v
trap - EXIT  # Remove trap para evitar chamada dupla

run_plot "$(pwd)" "$RESULTS_DIR"

info "EXP01 concluído. Resultados em: $RESULTS_DIR/"
