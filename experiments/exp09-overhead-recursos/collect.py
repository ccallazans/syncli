"""
collect.py – EXP09: Overhead de recursos (CPU e memória)
Lê os arquivos .tsv gerados pelo docker stats e calcula médias e picos
de CPU% e memória para cada condição operacional.
Salva em results/recursos.csv.

Formato das linhas TSV:
  campo 1: CPU%  (ex.: "12.50")
  campo 2: MemUsage (ex.: "45.2MiB / 4GiB")
"""
import re
import csv
import numpy as np
from pathlib import Path

RESULTS = Path("results")

# --------------------------------------------------------------------------
# Converte string de memória para bytes
# Suporta: B, KiB, MiB, GiB, KB, MB, GB
# --------------------------------------------------------------------------
MEM_RE = re.compile(
    r"([\d.]+)\s*(GiB|MiB|KiB|GB|MB|KB|B)", re.IGNORECASE
)
MULTIPLIERS = {
    "gib": 1024 ** 3,
    "mib": 1024 ** 2,
    "kib": 1024,
    "gb":  1000 ** 3,
    "mb":  1000 ** 2,
    "kb":  1000,
    "b":   1,
}

def mem_to_bytes(s: str) -> float:
    """Extrai o primeiro valor de memória (antes do '/') e converte para bytes."""
    # Pega apenas a parte antes da '/'
    part = s.split("/")[0].strip()
    m = MEM_RE.search(part)
    if not m:
        return 0.0
    value = float(m.group(1))
    unit  = m.group(2).lower()
    return value * MULTIPLIERS.get(unit, 1)


# --------------------------------------------------------------------------
# Extrai condição e n_files do nome do arquivo .tsv
# Exemplos de nomes:
#   stats_repouso_pequeno.tsv  → condicao="repouso_pequeno", n_files=10
#   stats_atividade.tsv        → condicao="atividade",        n_files=0
#   stats_repouso_100.tsv      → condicao="repouso_100",      n_files=100
# --------------------------------------------------------------------------
TSV_PATTERN = re.compile(r"stats_(.+)\.tsv$")

def parse_tsv(path: Path):
    """Lê TSV e retorna (cpu_list, mem_bytes_list)."""
    cpus = []
    mems = []
    with open(path, encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            parts = line.split("\t", 1)
            if len(parts) < 2:
                continue
            try:
                cpu = float(parts[0].strip().replace("%", ""))
                mem = mem_to_bytes(parts[1])
                cpus.append(cpu)
                mems.append(mem)
            except (ValueError, AttributeError):
                continue
    return cpus, mems


# --------------------------------------------------------------------------
# Processa todos os TSVs
# --------------------------------------------------------------------------
rows = []

for tsv_path in sorted(RESULTS.glob("stats_*.tsv")):
    m = TSV_PATTERN.search(tsv_path.name)
    if not m:
        continue

    condicao = m.group(1)           # ex.: "repouso_pequeno", "atividade", "repouso_100"
    cpus, mems = parse_tsv(tsv_path)

    if not cpus:
        print(f"AVISO: {tsv_path.name} vazio ou sem dados válidos; pulando.")
        continue

    # Determina n_files a partir do nome da condição
    n_files_match = re.search(r"(\d+)$", condicao)
    if n_files_match:
        n_files = int(n_files_match.group(1))
    elif condicao == "repouso_pequeno":
        n_files = 10
    else:
        n_files = 0

    cpu_arr = np.array(cpus, dtype=float)
    mem_arr = np.array(mems, dtype=float)

    rows.append({
        "condicao":      condicao,
        "n_files":       n_files,
        "cpu_mean_pct":  round(float(np.mean(cpu_arr)),    3),
        "cpu_peak_pct":  round(float(np.max(cpu_arr)),     3),
        "mem_mean_mb":   round(float(np.mean(mem_arr)) / (1024 ** 2), 3),
        "mem_peak_mb":   round(float(np.max(mem_arr))  / (1024 ** 2), 3),
        "n_samples":     len(cpus),
    })

if not rows:
    raise SystemExit("ERRO: nenhum arquivo stats_*.tsv encontrado ou todos vazios.")

# Ordena por n_files para facilitar plots de escala
rows.sort(key=lambda r: (r["n_files"], r["condicao"]))

# --------------------------------------------------------------------------
# Salva CSV
# --------------------------------------------------------------------------
out_path = RESULTS / "recursos.csv"
fieldnames = ["condicao", "n_files", "cpu_mean_pct", "cpu_peak_pct",
              "mem_mean_mb", "mem_peak_mb", "n_samples"]

with open(out_path, "w", newline="", encoding="utf-8") as f:
    writer = csv.DictWriter(f, fieldnames=fieldnames)
    writer.writeheader()
    writer.writerows(rows)

print(f"Condições processadas: {[r['condicao'] for r in rows]}")
print(f"CSV salvo em: {out_path}")
