#!/usr/bin/env bash
# run.sh – EXP05: Transferência diferencial (opDelta vs opWrite)
# Compara bytes_wire entre envio integral e diferencial em dois cenários:
#   Caso (a): modificar bloco no MEIO → espera-se opDelta (delta pequeno)
#   Caso (b): inserir bytes no INÍCIO → espera-se opWrite (delta >= conteúdo)
set -euo pipefail
cd "$(dirname "$0")"

source ../common/helpers.sh

# --------------------------------------------------------------------------
# Configuração
# --------------------------------------------------------------------------
REPO_ROOT="$(git rev-parse --show-toplevel)"
RESULTS_DIR="$(pwd)/results"
REPS="${REPS:-5}"

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
bash ../common/metadata.sh syncli-exp05_expnet > "$RESULTS_DIR/metadata.json" 2>/dev/null || \
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

sleep 1

wait_peers_ready "$RESULTS_DIR/nodeA.jsonl" "$RESULTS_DIR/nodeB.jsonl" 60

# --------------------------------------------------------------------------
# Fase 1: cria arquivo base de 10 MB (popula cache de nodeA)
# --------------------------------------------------------------------------
info "Criando arquivo base de 10 MB (/dev/urandom)..."
start_base=$(current_line "$RESULTS_DIR/nodeA.jsonl")
docker exec syncli-exp05-nodeA-1 sh -c \
    "dd if=/dev/urandom of=/sync/base_10m.bin bs=1M count=10 2>/dev/null"

info "Aguardando ack_recv do arquivo base..."
wait_event "ack_recv" "$RESULTS_DIR/nodeA.jsonl" "$start_base" \
    "base_10m.bin" 120 > /dev/null
info "Cache de nodeA populado com base_10m.bin."

# --------------------------------------------------------------------------
# Fase 2: Caso (a) – modificar bloco no MEIO
# --------------------------------------------------------------------------
info "=== Caso (a): modificar bloco no meio (${REPS} repetições) ==="

for rep in $(seq 1 "$REPS"); do
    fname="caso_a_$(printf '%03d' "$rep").bin"
    info "  [Caso A] rep ${rep}/${REPS}: ${fname}"

    # Cria cópia do arquivo base
    start_first=$(current_line "$RESULTS_DIR/nodeA.jsonl")
    docker exec syncli-exp05-nodeA-1 sh -c \
        "dd if=/sync/base_10m.bin of=/sync/${fname} bs=1M 2>/dev/null"

    # Aguarda 1ª transferência (opWrite, popula cache para esse path)
    wait_event "ack_recv" "$RESULTS_DIR/nodeA.jsonl" "$start_first" \
        "${fname}" 120 > /dev/null
    info "    1ª transferência concluída (cache populado)"

    # Modifica 1 bloco no MEIO (offset 5 MB = seek=80 blocos de 64 KB)
    start_second=$(current_line "$RESULTS_DIR/nodeA.jsonl")
    docker exec syncli-exp05-nodeA-1 sh -c \
        "dd if=/dev/urandom of=/sync/${fname} bs=64K count=1 seek=80 conv=notrunc 2>/dev/null"

    # Aguarda 2ª transferência (esperado: opDelta)
    wait_event "ack_recv" "$RESULTS_DIR/nodeA.jsonl" "$start_second" \
        "${fname}" 120 > /dev/null
    info "    2ª transferência concluída"
done

# --------------------------------------------------------------------------
# Fase 3: Caso (b) – inserir bytes no MEIO
# --------------------------------------------------------------------------
info "=== Caso (b): inserir bytes no meio (${REPS} repetições) ==="

for rep in $(seq 1 "$REPS"); do
    fname="caso_b_$(printf '%03d' "$rep").bin"
    info "  [Caso B] rep ${rep}/${REPS}: ${fname}"

    # Cria cópia do arquivo base
    start_first=$(current_line "$RESULTS_DIR/nodeA.jsonl")
    docker exec syncli-exp05-nodeA-1 sh -c \
        "dd if=/sync/base_10m.bin of=/sync/${fname} bs=1M 2>/dev/null"

    # Aguarda 1ª transferência (opWrite, popula cache para esse path)
    wait_event "ack_recv" "$RESULTS_DIR/nodeA.jsonl" "$start_first" \
        "${fname}" 120 > /dev/null
    info "    1ª transferência concluída (cache populado)"

    # Insere 1 KB no MEIO (offset 5 MB = 80 blocos de 64 KB):
    # primeiros 5 MB intactos + 1 KB de zeros + restante do arquivo
    start_second=$(current_line "$RESULTS_DIR/nodeA.jsonl")
    docker exec syncli-exp05-nodeA-1 sh -c \
        "dd if=/sync/${fname} bs=1M count=5 2>/dev/null > /tmp/novo.bin && \
         dd if=/dev/zero bs=1K count=1 2>/dev/null >> /tmp/novo.bin && \
         dd if=/sync/${fname} bs=1M skip=5 2>/dev/null >> /tmp/novo.bin && \
         mv /tmp/novo.bin /sync/${fname}"

    # Aguarda 2ª transferência (esperado: opDelta ~5 MB pois blocos 80+ deslocam)
    wait_event "ack_recv" "$RESULTS_DIR/nodeA.jsonl" "$start_second" \
        "${fname}" 120 > /dev/null
    info "    2ª transferência concluída"
done

# --------------------------------------------------------------------------
# Fase 4: Caso (c) – inserir bytes no INÍCIO
# --------------------------------------------------------------------------
info "=== Caso (c): inserir bytes no início (${REPS} repetições) ==="

for rep in $(seq 1 "$REPS"); do
    fname="caso_c_$(printf '%03d' "$rep").bin"
    info "  [Caso C] rep ${rep}/${REPS}: ${fname}"

    # Cria cópia do arquivo base
    start_first=$(current_line "$RESULTS_DIR/nodeA.jsonl")
    docker exec syncli-exp05-nodeA-1 sh -c \
        "dd if=/sync/base_10m.bin of=/sync/${fname} bs=1M 2>/dev/null"

    # Aguarda 1ª transferência (opWrite, popula cache para esse path)
    wait_event "ack_recv" "$RESULTS_DIR/nodeA.jsonl" "$start_first" \
        "${fname}" 120 > /dev/null
    info "    1ª transferência concluída (cache populado)"

    # Insere 1 KB no início: todos os blocos deslocam de posição
    start_second=$(current_line "$RESULTS_DIR/nodeA.jsonl")
    docker exec syncli-exp05-nodeA-1 sh -c \
        "dd if=/dev/zero bs=1K count=1 2>/dev/null | cat - /sync/${fname} > /tmp/novo.bin && mv /tmp/novo.bin /sync/${fname}"

    # Aguarda 2ª transferência (esperado: opWrite pois delta >= conteúdo)
    wait_event "ack_recv" "$RESULTS_DIR/nodeA.jsonl" "$start_second" \
        "${fname}" 120 > /dev/null
    info "    2ª transferência concluída"
done

# --------------------------------------------------------------------------
# Coleta e geração de gráficos
# --------------------------------------------------------------------------
docker compose down -v
trap - EXIT  # Remove trap para evitar chamada dupla

run_plot "$(pwd)" "$RESULTS_DIR"

info "EXP05 concluído. Resultados em: $RESULTS_DIR/"
