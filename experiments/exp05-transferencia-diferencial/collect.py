"""
collect.py – EXP05: Transferência diferencial
Lê o log JSONL de nodeA e extrai, para cada arquivo caso_[ab]_NNN.bin,
os bytes_wire e a operação das duas transferências (1ª e 2ª envio).
Salva em results/diferencial.csv.
"""
import re
import csv
from pathlib import Path

from parse_explog import load_jsonl, filter_events

RESULTS = Path("results")
PATTERN = re.compile(r"caso_([abc])_(\d+)\.bin$")

# --------------------------------------------------------------------------
# Carrega log de nodeA
# --------------------------------------------------------------------------
dfA = load_jsonl(str(RESULTS / "nodeA.jsonl"))

if dfA.empty:
    raise SystemExit("ERRO: nodeA.jsonl vazio ou ausente.")

# --------------------------------------------------------------------------
# Filtra send_start para arquivos caso_[ab]
# --------------------------------------------------------------------------
send_starts = filter_events(dfA, "send_start")

# Agrupa por path: (caso, rep) -> lista de send_start ordenada por ts_ns
from collections import defaultdict
grupos = defaultdict(list)

for _, row in send_starts.iterrows():
    path = str(row.get("path", ""))
    m = PATTERN.search(path)
    if not m:
        continue
    caso = m.group(1)
    rep  = int(m.group(2))
    grupos[(caso, rep)].append(row)

# --------------------------------------------------------------------------
# Monta linhas do CSV
# --------------------------------------------------------------------------
rows = []

for (caso, rep), envios in sorted(grupos.items()):
    # Ordena por ts_ns para garantir ordem cronológica
    envios_sorted = sorted(envios, key=lambda r: int(r["ts_ns"]))

    # Cada transferência lógica dispara um send_start por peer; deduplica por version
    seen_versions = {}
    for e in envios_sorted:
        v = e.get("version")
        if v not in seen_versions:
            seen_versions[v] = e
    unique_transfers = list(seen_versions.values())

    if len(unique_transfers) < 2:
        # Sem par completo (1ª e 2ª transferência), ignora
        continue

    first  = unique_transfers[0]
    second = unique_transfers[1]

    op_first         = first.get("op", "")
    bytes_wire_first = first.get("bytes_wire", None)
    op_second        = second.get("op", "")
    bytes_wire_second = second.get("bytes_wire", None)

    # Percentual de redução: quanto menor o 2º envio em relação ao 1º
    reducao_pct = None
    if bytes_wire_first and bytes_wire_second and float(bytes_wire_first) > 0:
        reducao_pct = round(
            (1.0 - float(bytes_wire_second) / float(bytes_wire_first)) * 100.0,
            2
        )

    rows.append({
        "caso":              caso,
        "rep":               rep,
        "op_first":          op_first,
        "bytes_wire_first":  bytes_wire_first,
        "op_second":         op_second,
        "bytes_wire_second": bytes_wire_second,
        "reducao_pct":       reducao_pct,
    })

if not rows:
    raise SystemExit("ERRO: nenhuma amostra encontrada nos logs. Verifique nodeA.jsonl.")

# --------------------------------------------------------------------------
# Salva CSV
# --------------------------------------------------------------------------
out_path = RESULTS / "diferencial.csv"
fieldnames = [
    "caso", "rep",
    "op_first", "bytes_wire_first",
    "op_second", "bytes_wire_second",
    "reducao_pct",
]

with open(out_path, "w", newline="", encoding="utf-8") as f:
    writer = csv.DictWriter(f, fieldnames=fieldnames)
    writer.writeheader()
    writer.writerows(rows)

print(f"Amostras coletadas: {len(rows)}")
print(f"CSV salvo em: {out_path}")
