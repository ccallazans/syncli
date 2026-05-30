#!/usr/bin/env bash
# run.sh – EXP04: Recuperação após falha de par
# Mede tempo de detecção de queda, redescoberta e ressincronização via snapshot.
set -euo pipefail
cd "$(dirname "$0")"

source ../common/helpers.sh

# --------------------------------------------------------------------------
# Configuração
# --------------------------------------------------------------------------
REPO_ROOT="$(git rev-parse --show-toplevel)"
RESULTS_DIR="$(pwd)/results"

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
rm -f "$RESULTS_DIR/timeline.json" "$RESULTS_DIR/recuperacao.csv"

# --------------------------------------------------------------------------
# Captura de metadados
# --------------------------------------------------------------------------
info "Capturando metadados..."
bash ../common/metadata.sh syncli-exp04_expnet > "$RESULTS_DIR/metadata.json" 2>/dev/null || \
    bash ../common/metadata.sh > "$RESULTS_DIR/metadata.json" || true

# --------------------------------------------------------------------------
# Build das imagens
# --------------------------------------------------------------------------
build_images "$REPO_ROOT"

# --------------------------------------------------------------------------
# 1. Inicia ambos os nós e aguarda peer_discovered
# --------------------------------------------------------------------------
info "Iniciando containers..."
docker compose up -d

# Aguarda logs existirem
sleep 1

wait_peers_ready "$RESULTS_DIR/nodeA.jsonl" "$RESULTS_DIR/nodeB.jsonl" 60
info "Ambos os nós descobertos."

# --------------------------------------------------------------------------
# 2. Cria arquivo inicial em nodeA e aguarda ack_recv
# --------------------------------------------------------------------------
info "Criando arquivo baseline.txt em nodeA..."
start_line_A=$(current_line "$RESULTS_DIR/nodeA.jsonl")

docker exec syncli-exp04-nodeA-1 sh -c \
    "printf 'conteudo_inicial' > /sync/baseline.txt"

info "Aguardando ack_recv de baseline.txt em nodeA..."
wait_event "ack_recv" "$RESULTS_DIR/nodeA.jsonl" "$start_line_A" \
    "baseline.txt" 60 > /dev/null

info "baseline.txt sincronizado com sucesso."

# --------------------------------------------------------------------------
# 4. Registra ts_stop antes de parar nodeB
# --------------------------------------------------------------------------
ts_stop=$(date +%s%N)
info "Parando nodeB (ts_stop=${ts_stop})..."

# --------------------------------------------------------------------------
# 5. Para nodeB
# --------------------------------------------------------------------------
start_line_A_stop=$(current_line "$RESULTS_DIR/nodeA.jsonl")
docker compose stop nodeB

# --------------------------------------------------------------------------
# 6. Aguarda peer_removed em nodeA.jsonl (timeout 60s)
# --------------------------------------------------------------------------
info "Aguardando peer_removed em nodeA (timeout 60s)..."
removed_line=$(wait_event "peer_removed" "$RESULTS_DIR/nodeA.jsonl" \
    "$start_line_A_stop" "" 60)

ts_removed=$(echo "$removed_line" | grep -o '"ts_ns":[0-9]*' | grep -o '[0-9]*' || echo "0")
info "peer_removed detectado (ts_removed=${ts_removed})."

# --------------------------------------------------------------------------
# 8. Cria arquivo novo em nodeA enquanto nodeB está offline
# --------------------------------------------------------------------------
info "Criando offline_write.txt em nodeA (nodeB offline)..."
start_line_A_offline=$(current_line "$RESULTS_DIR/nodeA.jsonl")

docker exec syncli-exp04-nodeA-1 sh -c \
    "printf 'escrito_enquanto_offline' > /sync/offline_write.txt"

# Aguarda debounce_out de offline_write.txt (mas NÃO aguarda ack_recv)
info "Aguardando debounce_out de offline_write.txt..."
wait_event "debounce_out" "$RESULTS_DIR/nodeA.jsonl" "$start_line_A_offline" \
    "offline_write.txt" 30 > /dev/null || \
    warn "debounce_out não detectado – continuando..."

# --------------------------------------------------------------------------
# 10. Registra ts_start_again antes de reiniciar nodeB
# --------------------------------------------------------------------------
ts_start_again=$(date +%s%N)
info "Reiniciando nodeB (ts_start_again=${ts_start_again})..."

# --------------------------------------------------------------------------
# 11. Reinicia nodeB
# --------------------------------------------------------------------------
start_line_A_restart=$(current_line "$RESULTS_DIR/nodeA.jsonl")
start_line_B_restart=$(current_line "$RESULTS_DIR/nodeB.jsonl" 2>/dev/null || echo 1)
docker compose start nodeB

# Aguarda log de nodeB existir novamente
sleep 2

# --------------------------------------------------------------------------
# 12. Aguarda peer_discovered em nodeA.jsonl (após ts_start_again)
# --------------------------------------------------------------------------
info "Aguardando peer_discovered em nodeA após reinício..."
disc_line=$(wait_event "peer_discovered" "$RESULTS_DIR/nodeA.jsonl" \
    "$start_line_A_restart" "" 60)

ts_peer_disc=$(echo "$disc_line" | grep -o '"ts_ns":[0-9]*' | grep -o '[0-9]*' || echo "0")
info "peer_discovered após reinício (ts_peer_disc=${ts_peer_disc})."

# --------------------------------------------------------------------------
# 13. Aguarda snapshot_end em nodeB.jsonl (timeout 60s)
# --------------------------------------------------------------------------
info "Aguardando snapshot_end em nodeB (timeout 60s)..."
snap_line=$(wait_event "snapshot_end" "$RESULTS_DIR/nodeB.jsonl" \
    "$start_line_B_restart" "" 60) || \
    { warn "snapshot_end não detectado – usando ts atual."; snap_line=""; }

if [[ -n "$snap_line" ]]; then
    ts_snapshot_end=$(echo "$snap_line" | grep -o '"ts_ns":[0-9]*' | grep -o '[0-9]*' || echo "0")
else
    ts_snapshot_end=$(date +%s%N)
fi
info "snapshot_end (ts_snapshot_end=${ts_snapshot_end})."

# --------------------------------------------------------------------------
# 15. Verifica se offline_write.txt chegou em nodeB
# --------------------------------------------------------------------------
info "Verificando offline_write.txt em nodeB..."
OFFLINE_CONTENT=$(docker exec syncli-exp04-nodeB-1 \
    cat /sync/offline_write.txt 2>/dev/null || echo "AUSENTE")

if [[ "$OFFLINE_CONTENT" == "escrito_enquanto_offline" ]]; then
    info "offline_write.txt recebido corretamente em nodeB."
    OFFLINE_OK=1
else
    warn "offline_write.txt ausente ou com conteúdo incorreto em nodeB: '${OFFLINE_CONTENT}'"
    OFFLINE_OK=0
fi

# --------------------------------------------------------------------------
# 16. Salva timeline.json em results/
# --------------------------------------------------------------------------
info "Salvando timeline.json..."

# Cria JSON manualmente (sem dependência de jq no host)
cat > "$RESULTS_DIR/timeline.json" <<EOF
{
  "ts_stop":          ${ts_stop},
  "ts_removed":       ${ts_removed},
  "ts_start_again":   ${ts_start_again},
  "ts_peer_disc":     ${ts_peer_disc},
  "ts_snapshot_end":  ${ts_snapshot_end},
  "offline_write_ok": ${OFFLINE_OK}
}
EOF

info "timeline.json salvo."

# --------------------------------------------------------------------------
# Encerra containers e gera gráficos
# --------------------------------------------------------------------------
docker compose down -v
trap - EXIT  # Remove trap para evitar chamada dupla

run_plot "$(pwd)" "$RESULTS_DIR"

info "EXP04 concluído. Resultados em: $RESULTS_DIR/"
