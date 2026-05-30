#!/usr/bin/env bash
# run.sh – EXP09: Overhead de recursos (CPU e memória)
# Mede CPU% e memória do Syncli em 3 condições: repouso-pequeno,
# atividade contínua e repouso com N arquivos monitorados.
set -euo pipefail
cd "$(dirname "$0")"

source ../common/helpers.sh

# --------------------------------------------------------------------------
# Configuração
# --------------------------------------------------------------------------
REPO_ROOT="$(git rev-parse --show-toplevel)"
RESULTS_DIR="$(pwd)/results"
N_SAMPLES=60
SAMPLE_INTERVAL=2
SETTLE_WAIT=5

# --------------------------------------------------------------------------
# Parsing de linha do docker stats
# Entrada: "12.5%\t45.2MiB / 4GiB"
# Saída: linha formatada "12.5\t45.2MiB / 4GiB" (sem %)
# --------------------------------------------------------------------------
parse_stats_line() {
    local line="$1"
    local cpu
    local mem
    cpu=$(printf '%s' "$line" | awk -F'\t' '{print $1}' | tr -d '%')
    mem=$(printf '%s' "$line" | awk -F'\t' '{print $2}')
    printf '%s\t%s\n' "$cpu" "$mem"
}

# --------------------------------------------------------------------------
# Coleta N_SAMPLES amostras de docker stats para container, grava em arquivo
# --------------------------------------------------------------------------
collect_stats() {
    local container="$1"
    local outfile="$2"
    local n="${3:-$N_SAMPLES}"
    local interval="${4:-$SAMPLE_INTERVAL}"
    info "Coletando ${n} amostras de ${container} → $(basename "$outfile")"
    for _ in $(seq 1 "$n"); do
        local line
        line=$(docker stats --no-stream "$container" \
            --format '{{.CPUPerc}}\t{{.MemUsage}}' 2>/dev/null || echo "0%\t0MiB / 0GiB")
        parse_stats_line "$line" >> "$outfile"
        sleep "$interval"
    done
}

# --------------------------------------------------------------------------
# Aguarda peer_discovered em nodeA (par único, nodeB)
# --------------------------------------------------------------------------
wait_peer_ready_single() {
    local logA="$1"
    local timeout="${2:-60}"
    local deadline=$(( $(date +%s) + timeout ))
    info "Aguardando par descoberto em nodeA..."
    while [[ $(date +%s) -lt $deadline ]]; do
        local cnt
        cnt=$(grep -c '"peer_discovered"' "$logA" 2>/dev/null) || cnt=0
        if [[ "$cnt" -ge 1 ]]; then
            info "Par descoberto."
            return 0
        fi
        sleep 0.5
    done
    error "Timeout aguardando peer_discovered em $logA"
    return 1
}

# --------------------------------------------------------------------------
# Cleanup garantido
# --------------------------------------------------------------------------
WRITE_LOOP_PID=""
cleanup() {
    info "Encerrando containers e processos auxiliares..."
    if [[ -n "$WRITE_LOOP_PID" ]] && kill -0 "$WRITE_LOOP_PID" 2>/dev/null; then
        kill "$WRITE_LOOP_PID" 2>/dev/null || true
    fi
    docker compose down -v 2>/dev/null || true
}
trap cleanup EXIT

# --------------------------------------------------------------------------
# Preparação de diretórios
# --------------------------------------------------------------------------
mkdir -p "$RESULTS_DIR/sync_A" "$RESULTS_DIR/sync_B"

# --------------------------------------------------------------------------
# Captura de metadados
# --------------------------------------------------------------------------
info "Capturando metadados..."
bash ../common/metadata.sh syncli-exp09_expnet > "$RESULTS_DIR/metadata.json" 2>/dev/null || \
    bash ../common/metadata.sh > "$RESULTS_DIR/metadata.json" || true

# --------------------------------------------------------------------------
# Build das imagens
# --------------------------------------------------------------------------
build_images "$REPO_ROOT"

# ==========================================================================
# CONDIÇÃO 1 – Repouso-pequeno (10 arquivos, sem atividade)
# ==========================================================================
info "=========================================="
info "Condição 1: Repouso-pequeno (10 arquivos)"
info "=========================================="

rm -f "$RESULTS_DIR/nodeA.jsonl" "$RESULTS_DIR/nodeB.jsonl"
rm -f "$RESULTS_DIR/sync_A/"* "$RESULTS_DIR/sync_B/"* 2>/dev/null || true

# Cria 10 arquivos pequenos no diretório de sync
for i in $(seq 1 10); do
    dd if=/dev/zero of="$RESULTS_DIR/sync_A/seed_$(printf '%03d' "$i").bin" \
        bs=1K count=1 2>/dev/null
done

docker compose up -d nodeA nodeB
sleep 1
wait_peer_ready_single "$RESULTS_DIR/nodeA.jsonl" 60

info "Aguardando estabilização (${SETTLE_WAIT}s)..."
sleep "$SETTLE_WAIT"

collect_stats "syncli-exp09-nodeA-1" \
    "$RESULTS_DIR/stats_repouso_pequeno.tsv"

docker compose down -v
rm -f "$RESULTS_DIR/nodeA.jsonl" "$RESULTS_DIR/nodeB.jsonl"

# ==========================================================================
# CONDIÇÃO 2 – Atividade (arquivo novo a cada 500ms por 60s)
# ==========================================================================
info "=========================================="
info "Condição 2: Atividade contínua (60s)"
info "=========================================="

rm -f "$RESULTS_DIR/sync_A/"* "$RESULTS_DIR/sync_B/"* 2>/dev/null || true

docker compose up -d nodeA nodeB
sleep 1
wait_peer_ready_single "$RESULTS_DIR/nodeA.jsonl" 60

info "Iniciando loop de escrita em background..."
{
    seq_n=0
    end=$(( $(date +%s) + 60 ))
    while [[ $(date +%s) -lt $end ]]; do
        seq_n=$(( seq_n + 1 ))
        docker exec syncli-exp09-nodeA-1 sh -c \
            "dd if=/dev/zero of=/sync/atividade_$(printf '%05d' "$seq_n").bin bs=4K count=1 2>/dev/null" \
            2>/dev/null || true
        sleep 0.5
    done
} &
WRITE_LOOP_PID=$!

collect_stats "syncli-exp09-nodeA-1" \
    "$RESULTS_DIR/stats_atividade.tsv"

# Aguarda loop de escrita terminar (pode já ter terminado)
wait "$WRITE_LOOP_PID" 2>/dev/null || true
WRITE_LOOP_PID=""

docker compose down -v
rm -f "$RESULTS_DIR/nodeA.jsonl" "$RESULTS_DIR/nodeB.jsonl"

# ==========================================================================
# CONDIÇÃO 3 – Repouso em escala de watches (APENAS nodeA)
# ==========================================================================
for N_FILES in 100 1000 5000; do
    info "=========================================="
    info "Condição 3: Repouso-escala N=${N_FILES} arquivos"
    info "=========================================="

    rm -f "$RESULTS_DIR/sync_A/"* 2>/dev/null || true
    find "$RESULTS_DIR/sync_A" -mindepth 1 -delete 2>/dev/null || true

    # Cria N_FILES arquivos em N_DIRS subdiretórios (10 arquivos por dir)
    N_DIRS=$(( N_FILES / 10 ))
    info "Criando ${N_FILES} arquivos em ${N_DIRS} subdiretórios..."
    for d in $(seq 1 "$N_DIRS"); do
        dir_path="$RESULTS_DIR/sync_A/dir_$(printf '%05d' "$d")"
        mkdir -p "$dir_path"
        for f in $(seq 1 10); do
            dd if=/dev/zero \
               of="${dir_path}/file_$(printf '%03d' "$f").bin" \
               bs=1K count=1 2>/dev/null
        done
    done

    rm -f "$RESULTS_DIR/nodeA.jsonl"

    # Sobe APENAS nodeA (sem nodeB – mede overhead de watching puro)
    SYNC_A_DIR="$RESULTS_DIR/sync_A" docker compose up -d nodeA
    sleep 1

    info "Aguardando estabilização (${SETTLE_WAIT}s)..."
    sleep "$SETTLE_WAIT"

    collect_stats "syncli-exp09-nodeA-1" \
        "$RESULTS_DIR/stats_repouso_${N_FILES}.tsv"

    docker compose down -v
    rm -f "$RESULTS_DIR/nodeA.jsonl"
done

# Cleanup final
trap - EXIT

# --------------------------------------------------------------------------
# Coleta e geração de gráficos
# --------------------------------------------------------------------------
run_plot "$(pwd)" "$RESULTS_DIR"

info "EXP09 concluído. Resultados em: $RESULTS_DIR/"
