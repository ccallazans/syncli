#!/usr/bin/env bash
# helpers.sh – funções auxiliares para scripts de experimento.
# Uso: source "$(dirname "$0")/../common/helpers.sh"
#
# NOTA: Este arquivo é carregado por scripts que usam `set -euo pipefail`.
# Todas as funções devem ser robustas a "set -e" e "pipefail":
#   - grep sem match retorna 1 → usar `|| result=""` em atribuições com pipe
#   - grep -sc em arquivo inexistente pode retornar "0\n0" → verificar -f primeiro

# Cores para output
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }

# --------------------------------------------------------------------------
# build_images – constrói imagens Docker (idempotente, usa cache).
# Uso: build_images <repo_root>
# --------------------------------------------------------------------------
build_images() {
    local root="${1:?raiz do repositório obrigatória}"
    info "Construindo syncli-experiments-node..."
    docker build -q -t syncli-experiments-node \
        -f "$root/experiments/common/Dockerfile.syncli" "$root" \
        || { error "Falha ao construir syncli-experiments-node"; return 1; }

    info "Construindo syncli-experiments-plot..."
    docker build -q -t syncli-experiments-plot \
        -f "$root/experiments/common/Dockerfile.plot" "$root/experiments/common/" \
        || { error "Falha ao construir syncli-experiments-plot"; return 1; }
    info "Imagens prontas."
}

# --------------------------------------------------------------------------
# _grep_count – conta linhas que batem com pattern em arquivo, sem falhar.
# Retorna 0 (numérico) se o arquivo não existe ou não tem matches.
# --------------------------------------------------------------------------
_grep_count() {
    local pattern="$1" file="$2"
    [[ ! -f "$file" ]] && echo 0 && return 0
    # grep -c imprime "0" e sai com código 1 quando não há match.
    # Usar atribuição com || para capturar o count sem disparar set -e.
    local cnt
    cnt=$(grep -c "$pattern" "$file" 2>/dev/null) || cnt=0
    echo "$cnt"
}

# --------------------------------------------------------------------------
# wait_event – aguarda evento aparecer em JSONL após a linha start_line.
# Uso: wait_event <evento> <jsonl> <start_line> [path_filter] [timeout_s]
# Retorna: linha JSON do evento encontrado (via echo) ou exit 1 por timeout.
# IMPORTANTE: seguro com set -euo pipefail (grep sem match usa || result="")
# --------------------------------------------------------------------------
wait_event() {
    local event="$1"
    local logfile="$2"
    local start_line="${3:-1}"
    local path_filter="${4:-}"
    local timeout="${5:-45}"
    local deadline=$(( $(date +%s) + timeout ))

    while [[ $(date +%s) -lt $deadline ]]; do
        if [[ -f "$logfile" ]]; then
            local result=""
            if [[ -n "$path_filter" ]]; then
                # || result="" previne que grep sem match (exit 1 + pipefail) encerre o script
                result=$(awk "NR>=$start_line" "$logfile" 2>/dev/null \
                    | grep "\"event\":\"$event\"" \
                    | grep "\"path\":\"$path_filter\"" \
                    | tail -1) || result=""
            else
                result=$(awk "NR>=$start_line" "$logfile" 2>/dev/null \
                    | grep "\"event\":\"$event\"" \
                    | tail -1) || result=""
            fi
            if [[ -n "$result" ]]; then
                echo "$result"
                return 0
            fi
        fi
        sleep 0.1
    done
    error "Timeout (${timeout}s) aguardando evento '$event' (path='$path_filter') em $logfile"
    return 1
}

# --------------------------------------------------------------------------
# wait_peers_ready – aguarda peer_discovered em AMBOS os arquivos de log.
# Uso: wait_peers_ready <logA> <logB> [timeout_s]
# --------------------------------------------------------------------------
wait_peers_ready() {
    local logA="$1" logB="$2" timeout="${3:-60}"
    local deadline=$(( $(date +%s) + timeout ))
    info "Aguardando descoberta de pares..."
    while [[ $(date +%s) -lt $deadline ]]; do
        local okA okB
        okA=$(_grep_count '"peer_discovered"' "$logA")
        okB=$(_grep_count '"peer_discovered"' "$logB")
        if [[ "$okA" -ge 1 && "$okB" -ge 1 ]]; then
            info "Pares descobertos."
            return 0
        fi
        sleep 0.5
    done
    error "Timeout aguardando descoberta de pares."
    return 1
}

# --------------------------------------------------------------------------
# wait_stable – aguarda que nenhum JSONL receba novas linhas por stable_s.
# Uso: wait_stable <stable_s> <jsonl1> [jsonl2 ...]
# --------------------------------------------------------------------------
wait_stable() {
    local stable_s="${1:-1}"; shift
    local -a logs=("$@")
    local -a prev=()
    local f
    for f in "${logs[@]}"; do
        prev+=("$(wc -l < "$f" 2>/dev/null || echo 0)")
    done
    # awk para suportar stable_s fracionário (ex.: 1.5)
    local ticks=0
    local needed
    needed=$(awk "BEGIN { printf \"%d\", $stable_s * 10 + 0.5 }")
    while [[ $ticks -lt $needed ]]; do
        sleep 0.1
        local changed=0 i
        for i in "${!logs[@]}"; do
            local cur
            cur=$(wc -l < "${logs[$i]}" 2>/dev/null || echo 0)
            if [[ "$cur" != "${prev[$i]}" ]]; then
                prev[$i]="$cur"
                changed=1
            fi
        done
        if [[ $changed -eq 1 ]]; then
            ticks=0
        else
            ticks=$(( ticks + 1 ))
        fi
    done
}

# --------------------------------------------------------------------------
# current_line – retorna o número de linhas atual de um arquivo + 1.
# Usado para definir o ponto de início do polling em wait_event.
# --------------------------------------------------------------------------
current_line() {
    local f="$1"
    local n
    n=$(wc -l < "$f" 2>/dev/null || echo 0)
    echo $(( n + 1 ))
}

# --------------------------------------------------------------------------
# run_plot – executa collect.py e plot.py dentro do container de plot.
# Uso: run_plot <exp_dir> <results_dir>
# collect.py e plot.py devem estar em <exp_dir>.
# parse_explog.py e style.py estão em /app (já na imagem).
# --------------------------------------------------------------------------
run_plot() {
    local exp_dir="${1:?}"
    local results_dir="${2:?}"
    info "Executando coleta (collect.py)..."
    docker run --rm \
        -v "${exp_dir}:/exp:ro" \
        -v "${results_dir}:/exp/results" \
        -e PYTHONPATH=/app \
        syncli-experiments-plot \
        bash -c "cd /exp && python collect.py"

    info "Gerando gráficos (plot.py)..."
    docker run --rm \
        -v "${exp_dir}:/exp:ro" \
        -v "${results_dir}:/exp/results" \
        -e PYTHONPATH=/app \
        syncli-experiments-plot \
        bash -c "cd /exp && python plot.py"

    info "Gráficos salvos em $results_dir/"
}
