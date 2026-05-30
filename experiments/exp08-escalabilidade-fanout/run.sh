#!/usr/bin/env bash
# run.sh – EXP08: Escalabilidade do fan-out (2 → N nós)
# Mede como o tempo até o último ACK cresce com o número de pares.
set -euo pipefail
cd "$(dirname "$0")"

source ../common/helpers.sh

# --------------------------------------------------------------------------
# Configuração
# --------------------------------------------------------------------------
REPO_ROOT="$(git rev-parse --show-toplevel)"
RESULTS_DIR="$(pwd)/results"
REPS="${REPS:-10}"
N_LIST=(2 3 4 5 6 8)

# --------------------------------------------------------------------------
# Funções auxiliares
# --------------------------------------------------------------------------

# Aguarda que nodeA.jsonl contenha pelo menos n_peers eventos peer_discovered
wait_fanout_peers() {
    local logfile="$1"
    local n_peers="$2"
    local timeout="${3:-60}"
    local deadline=$(( $(date +%s) + timeout ))
    info "Aguardando $n_peers par(es) descobertos em nodeA..."
    while [[ $(date +%s) -lt $deadline ]]; do
        local cnt
        cnt=$(grep -c '"peer_discovered"' "$logfile" 2>/dev/null) || cnt=0
        if [[ "$cnt" -ge "$n_peers" ]]; then
            info "$cnt par(es) descobertos."
            return 0
        fi
        sleep 0.5
    done
    error "Timeout aguardando $n_peers peer_discovered em $logfile"
    return 1
}

# Aguarda (n_acks) eventos ack_recv para o mesmo path após start_line
wait_all_acks() {
    local path="$1"
    local logfile="$2"
    local start_line="$3"
    local n_acks="$4"
    local timeout="${5:-45}"
    local deadline=$(( $(date +%s) + timeout ))
    while [[ $(date +%s) -lt $deadline ]]; do
        local cnt
        cnt=$(awk "NR>=$start_line" "$logfile" 2>/dev/null \
            | grep '"ack_recv"' \
            | grep "\"path\":\"$path\"" \
            | wc -l)
        [[ "$cnt" -ge "$n_acks" ]] && return 0
        sleep 0.1
    done
    error "Timeout (${timeout}s) aguardando $n_acks ack_recv para path='$path'"
    return 1
}

# --------------------------------------------------------------------------
# Cleanup garantido mesmo em caso de erro
# --------------------------------------------------------------------------
CPU_MONITOR_PID=""
cleanup() {
    info "Encerrando containers e processos de monitoramento..."
    if [[ -n "$CPU_MONITOR_PID" ]] && kill -0 "$CPU_MONITOR_PID" 2>/dev/null; then
        kill "$CPU_MONITOR_PID" 2>/dev/null || true
    fi
    docker compose down -v 2>/dev/null || true
}
trap cleanup EXIT

# --------------------------------------------------------------------------
# Preparação de diretórios e logs
# --------------------------------------------------------------------------
mkdir -p "$RESULTS_DIR"
for i in A 1 2 3 4 5 6 7; do
    mkdir -p "$RESULTS_DIR/sync_${i}"
done

rm -f "$RESULTS_DIR"/nodeA.jsonl "$RESULTS_DIR"/node*.jsonl
rm -f "$RESULTS_DIR"/sync_A/* "$RESULTS_DIR"/sync_[1-7]/* 2>/dev/null || true
rm -f "$RESULTS_DIR/cpu_nodeA.txt"

# --------------------------------------------------------------------------
# Captura de metadados
# --------------------------------------------------------------------------
info "Capturando metadados..."
bash ../common/metadata.sh syncli-exp08_expnet > "$RESULTS_DIR/metadata.json" 2>/dev/null || \
    bash ../common/metadata.sh > "$RESULTS_DIR/metadata.json" || true

# --------------------------------------------------------------------------
# Build das imagens
# --------------------------------------------------------------------------
build_images "$REPO_ROOT"

# --------------------------------------------------------------------------
# Monitor de CPU em background (durante todo o experimento)
# --------------------------------------------------------------------------
{
    while true; do
        docker stats --no-stream syncli-exp08-nodeA-1 \
            --format '{{.CPUPerc}}' 2>/dev/null >> "$RESULTS_DIR/cpu_nodeA.txt" || true
        sleep 2
    done
} &
CPU_MONITOR_PID=$!
info "Monitor de CPU iniciado (PID=$CPU_MONITOR_PID)"

# --------------------------------------------------------------------------
# Loop principal: para cada N (número total de nós)
# --------------------------------------------------------------------------
for N in "${N_LIST[@]}"; do
    n_peers=$(( N - 1 ))
    info "=========================================="
    info "Configuração: N=$N nós ($n_peers par(es))"
    info "=========================================="

    # Determina serviços a subir: nodeA + node1..node(N-1)
    SERVICES="nodeA"
    for i in $(seq 1 "$n_peers"); do
        SERVICES="$SERVICES node${i}"
    done

    # Limpa JSONLs antes de subir
    rm -f "$RESULTS_DIR"/nodeA.jsonl "$RESULTS_DIR"/node*.jsonl
    rm -f "$RESULTS_DIR"/sync_A/* 2>/dev/null || true
    for i in $(seq 1 "$n_peers"); do
        rm -f "$RESULTS_DIR/sync_${i}/"* 2>/dev/null || true
    done

    # Sobe serviços necessários
    # shellcheck disable=SC2086
    docker compose up -d $SERVICES
    sleep 1

    # Aguarda nodeA descobrir todos os pares
    wait_fanout_peers "$RESULTS_DIR/nodeA.jsonl" "$n_peers" 90

    # Loop de repetições
    for rep in $(seq 1 "$REPS"); do
        path_name="test_n${N}_r$(printf '%03d' "$rep").bin"

        # Registra linha atual antes de criar o arquivo
        start_line=$(current_line "$RESULTS_DIR/nodeA.jsonl")

        # Cria arquivo de 1 MB em nodeA
        docker exec syncli-exp08-nodeA-1 sh -c \
            "dd if=/dev/zero of=/sync/${path_name} bs=1M count=1 2>/dev/null"

        # Aguarda (N-1) ack_recv para esse path
        if wait_all_acks "${path_name}" "$RESULTS_DIR/nodeA.jsonl" \
                "$start_line" "$n_peers" 60; then
            info "  [N=${N}] rep ${rep}/${REPS} concluída"
        else
            warn "  [N=${N}] rep ${rep}/${REPS} – timeout; continuando"
        fi
    done

    # Salva JSONL de nodeA com tag de N antes de limpar para o próximo ciclo.
    # Usa prefixo "fanout_n" para não ser apagado pelo glob "node*.jsonl".
    [[ -f "$RESULTS_DIR/nodeA.jsonl" ]] && \
        cp "$RESULTS_DIR/nodeA.jsonl" "$RESULTS_DIR/fanout_n${N}.jsonl"

    # Para os serviços entre configurações
    info "Parando serviços para N=$N..."
    # shellcheck disable=SC2086
    docker compose stop $SERVICES
done

# --------------------------------------------------------------------------
# Derruba infraestrutura
# --------------------------------------------------------------------------
docker compose down -v
trap - EXIT

# Para monitor de CPU
if [[ -n "$CPU_MONITOR_PID" ]] && kill -0 "$CPU_MONITOR_PID" 2>/dev/null; then
    kill "$CPU_MONITOR_PID" 2>/dev/null || true
fi

# --------------------------------------------------------------------------
# Coleta e geração de gráficos
# --------------------------------------------------------------------------
run_plot "$(pwd)" "$RESULTS_DIR"

info "EXP08 concluído. Resultados em: $RESULTS_DIR/"
