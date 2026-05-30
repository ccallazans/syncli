#!/usr/bin/env bash
# run.sh – EXP03: Modificações concorrentes (LWW)
# Escreve conteúdo distinto em ambos os nós simultaneamente e verifica convergência.
set -euo pipefail
cd "$(dirname "$0")"

source ../common/helpers.sh

# --------------------------------------------------------------------------
# Configuração
# --------------------------------------------------------------------------
REPO_ROOT="$(git rev-parse --show-toplevel)"
RESULTS_DIR="$(pwd)/results"
REPS_CONC="${REPS_CONC:-30}"

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
rm -f "$RESULTS_DIR/raw.csv"

# --------------------------------------------------------------------------
# Captura de metadados
# --------------------------------------------------------------------------
info "Capturando metadados..."
bash ../common/metadata.sh syncli-exp03_expnet > "$RESULTS_DIR/metadata.json" 2>/dev/null || \
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
# Cria raw.csv com header
# --------------------------------------------------------------------------
echo "rep,hash_A,hash_B,converged,start_ts_ns,end_ts_ns" > "$RESULTS_DIR/raw.csv"

# --------------------------------------------------------------------------
# Loop de experimentos
# --------------------------------------------------------------------------
info "Iniciando ${REPS_CONC} repetições de escrita concorrente..."

for rep in $(seq 1 "$REPS_CONC"); do
    info "Repetição ${rep}/${REPS_CONC}..."

    # Remove arquivo de iteração anterior para garantir estado limpo
    docker exec syncli-exp03-nodeA-1 sh -c "rm -f /sync/concurrent.txt" 2>/dev/null || true
    docker exec syncli-exp03-nodeB-1 sh -c "rm -f /sync/concurrent.txt" 2>/dev/null || true

    # Aguarda estabilização após remoção
    wait_stable 1.5 "$RESULTS_DIR/nodeA.jsonl" "$RESULTS_DIR/nodeB.jsonl"

    # Captura timestamp de início da repetição (nanosegundos desde epoch)
    start_ts_ns=$(date +%s%N)

    # Escreve conteúdo DISTINTO em ambos os nós SIMULTANEAMENTE
    docker exec syncli-exp03-nodeA-1 sh -c "printf 'conteudo_A_rep${rep}' > /sync/concurrent.txt" &
    PID_A=$!
    docker exec syncli-exp03-nodeB-1 sh -c "printf 'conteudo_B_rep${rep}' > /sync/concurrent.txt" &
    PID_B=$!
    wait $PID_A $PID_B

    # Aguarda estabilização: sem novas linhas em AMBOS JSONLs por 1.5s
    wait_stable 1.5 "$RESULTS_DIR/nodeA.jsonl" "$RESULTS_DIR/nodeB.jsonl"

    # Captura timestamp de fim da repetição
    end_ts_ns=$(date +%s%N)

    # Compara hash do arquivo nos dois nós
    HASH_A=$(docker exec syncli-exp03-nodeA-1 sh -c \
        "sha256sum /sync/concurrent.txt 2>/dev/null | cut -d' ' -f1" 2>/dev/null || echo "absent")
    HASH_B=$(docker exec syncli-exp03-nodeB-1 sh -c \
        "sha256sum /sync/concurrent.txt 2>/dev/null | cut -d' ' -f1" 2>/dev/null || echo "absent")

    # Determina convergência
    if [[ "$HASH_A" == "$HASH_B" && "$HASH_A" != "absent" ]]; then
        CONVERGED=1
    else
        CONVERGED=0
    fi

    # Appenda linha ao raw.csv
    echo "${rep},${HASH_A},${HASH_B},${CONVERGED},${start_ts_ns},${end_ts_ns}" >> "$RESULTS_DIR/raw.csv"

    if [[ "$CONVERGED" -eq 1 ]]; then
        info "  rep ${rep}: CONVERGIU (hash=${HASH_A:0:12}...)"
    else
        warn "  rep ${rep}: DIVERGIU (hashA=${HASH_A:0:12}... hashB=${HASH_B:0:12}...)"
    fi
done

# --------------------------------------------------------------------------
# Encerra containers e gera gráficos
# --------------------------------------------------------------------------
docker compose down -v
trap - EXIT  # Remove trap para evitar chamada dupla

run_plot "$(pwd)" "$RESULTS_DIR"

info "EXP03 concluído. Resultados em: $RESULTS_DIR/"
