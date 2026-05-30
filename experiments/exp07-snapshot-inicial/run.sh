#!/usr/bin/env bash
# run.sh – EXP07: Snapshot inicial
# Mede o tempo de snapshot (snapshot_req → snapshot_end no cliente nodeB)
# para diferentes volumes de dados já presentes em nodeA.
set -euo pipefail
cd "$(dirname "$0")"

source ../common/helpers.sh

# --------------------------------------------------------------------------
# Configuração
# --------------------------------------------------------------------------
REPO_ROOT="$(git rev-parse --show-toplevel)"
RESULTS_DIR="$(pwd)/results"
REPS="${REPS:-5}"

# Volumes em MB e número correspondente de arquivos de 1 MB cada
VOLUMES_MB=(10 50 100 200)

# --------------------------------------------------------------------------
# Cleanup garantido mesmo em caso de erro
# --------------------------------------------------------------------------
cleanup() {
    info "Encerrando containers..."
    docker compose down -v 2>/dev/null || true
}
trap cleanup EXIT

# --------------------------------------------------------------------------
# Preparação de diretórios base
# --------------------------------------------------------------------------
mkdir -p "$RESULTS_DIR"
rm -f "$RESULTS_DIR/nodeA.jsonl"

# --------------------------------------------------------------------------
# Captura de metadados
# --------------------------------------------------------------------------
info "Capturando metadados..."
bash ../common/metadata.sh syncli-exp07_expnet > "$RESULTS_DIR/metadata.json" 2>/dev/null || \
    bash ../common/metadata.sh > "$RESULTS_DIR/metadata.json" || true

# --------------------------------------------------------------------------
# Build das imagens
# --------------------------------------------------------------------------
build_images "$REPO_ROOT"

# --------------------------------------------------------------------------
# Loop por volume
# --------------------------------------------------------------------------
for V_MB in "${VOLUMES_MB[@]}"; do
    N_FILES="$V_MB"  # 1 arquivo de 1 MB por unidade de volume
    SYNC_A="$RESULTS_DIR/sync_A_${V_MB}"

    info "=== Volume: ${V_MB} MB (${N_FILES} arquivos de 1 MB) ==="

    # Cria e popula diretório sync_A com V_MB arquivos (somente se não existir já)
    if [[ ! -d "$SYNC_A" ]] || [[ -z "$(ls -A "$SYNC_A" 2>/dev/null)" ]]; then
        mkdir -p "$SYNC_A"
        info "  Populando sync_A_${V_MB} com ${N_FILES} arquivos..."
        for i in $(seq 1 "$N_FILES"); do
            dd if=/dev/zero \
               of="${SYNC_A}/file_$(printf '%03d' "$i").bin" \
               bs=1M count=1 2>/dev/null
        done
        info "  Diretório populado."
    else
        info "  sync_A_${V_MB} já populado, reutilizando."
    fi

    # Limpa log de nodeA e inicia apenas nodeA
    rm -f "$RESULTS_DIR/nodeA.jsonl"
    export SYNC_A_DIR="$SYNC_A"
    export SYNC_B_DIR="$RESULTS_DIR/sync_B_tmp"
    export NODEB_LOG="/results/nodeB_${V_MB}_placeholder.jsonl"

    info "  Iniciando nodeA..."
    docker compose up -d nodeA

    # Aguarda 10s para que nodeA registre-se no mDNS
    info "  Aguardando 10s para registro mDNS de nodeA..."
    sleep 10

    # --------------------------------------------------------------------------
    # Loop de repetições para este volume
    # --------------------------------------------------------------------------
    for rep in $(seq 1 "$REPS"); do
        REP_PAD="$(printf '%03d' "$rep")"
        NODEB_JSONL="$RESULTS_DIR/nodeB_${V_MB}_${REP_PAD}.jsonl"

        info "  Rep ${rep}/${REPS}: iniciando nodeB (log: nodeB_${V_MB}_${REP_PAD}.jsonl)..."

        # Limpa diretório sync_B e log de nodeB
        rm -rf "$RESULTS_DIR/sync_B_tmp"
        mkdir -p "$RESULTS_DIR/sync_B_tmp"
        rm -f "$NODEB_JSONL"

        # Configura variáveis para o docker-compose
        export SYNC_A_DIR="$SYNC_A"
        export SYNC_B_DIR="$RESULTS_DIR/sync_B_tmp"
        export NODEB_LOG="/results/nodeB_${V_MB}_${REP_PAD}.jsonl"

        # Inicia nodeB
        docker compose up -d nodeB

        # Aguarda snapshot_end no log de nodeB (timeout 120s)
        info "    Aguardando snapshot_end..."
        wait_event "snapshot_end" "$NODEB_JSONL" 1 "" 120 > /dev/null
        info "    snapshot_end recebido."

        # Para nodeB para próxima rep
        docker compose stop nodeB
        docker compose rm -f nodeB 2>/dev/null || true
    done

    # Para nodeA após todas as reps deste volume
    docker compose stop nodeA
    docker compose rm -f nodeA 2>/dev/null || true
    info "  Volume ${V_MB} MB concluído."
done

# --------------------------------------------------------------------------
# Coleta e geração de gráficos
# --------------------------------------------------------------------------
docker compose down -v 2>/dev/null || true
trap - EXIT  # Remove trap para evitar chamada dupla

run_plot "$(pwd)" "$RESULTS_DIR"

info "EXP07 concluído. Resultados em: $RESULTS_DIR/"
