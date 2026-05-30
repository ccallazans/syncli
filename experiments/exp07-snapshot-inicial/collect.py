"""
collect.py – EXP07: Snapshot inicial
Lê os logs JSONL de nodeB gerados para cada combinação de volume/rep
(nodeB_${V_MB}_${rep}.jsonl) e extrai a duração do snapshot.
Salva em results/snapshot.csv.
"""
import re
import csv
from pathlib import Path

from parse_explog import load_jsonl, filter_events

RESULTS = Path("results")
PATTERN = re.compile(r"nodeB_(\d+)_(\d+)\.jsonl$")

# --------------------------------------------------------------------------
# Descobre todos os arquivos nodeB_*.jsonl em results/
# --------------------------------------------------------------------------
jsonl_files = sorted(RESULTS.glob("nodeB_*.jsonl"))

if not jsonl_files:
    raise SystemExit("ERRO: Nenhum arquivo nodeB_*.jsonl encontrado em results/.")

rows = []

for jsonl_path in jsonl_files:
    m = PATTERN.search(jsonl_path.name)
    if not m:
        continue

    volume_mb = int(m.group(1))
    rep       = int(m.group(2))

    df = load_jsonl(str(jsonl_path))
    if df.empty:
        print(f"  AVISO: {jsonl_path.name} vazio, ignorando.")
        continue

    # Extrai snapshot_req e snapshot_end (ambos emitidos pelo cliente = nodeB)
    snap_reqs = filter_events(df, "snapshot_req")
    snap_ends = filter_events(df, "snapshot_end")

    if snap_reqs.empty:
        print(f"  AVISO: snapshot_req ausente em {jsonl_path.name}, ignorando.")
        continue
    if snap_ends.empty:
        print(f"  AVISO: snapshot_end ausente em {jsonl_path.name}, ignorando.")
        continue

    # Pega o primeiro snapshot_req e o primeiro snapshot_end após ele
    req_row = snap_reqs.iloc[0]
    ts_req  = int(req_row["ts_ns"])

    end_candidates = snap_ends[snap_ends["ts_ns"] > ts_req]
    if end_candidates.empty:
        print(f"  AVISO: snapshot_end antes de snapshot_req em {jsonl_path.name}, ignorando.")
        continue

    end_row    = end_candidates.iloc[0]
    ts_end     = int(end_row["ts_ns"])
    duration_ms = round((ts_end - ts_req) / 1e6, 3)

    rows.append({
        "volume_mb":   volume_mb,
        "rep":         rep,
        "duration_ms": duration_ms,
    })

if not rows:
    raise SystemExit("ERRO: nenhuma amostra válida encontrada nos logs.")

# Ordena por volume e rep para consistência
rows.sort(key=lambda r: (r["volume_mb"], r["rep"]))

# --------------------------------------------------------------------------
# Salva CSV
# --------------------------------------------------------------------------
out_path = RESULTS / "snapshot.csv"
fieldnames = ["volume_mb", "rep", "duration_ms"]

with open(out_path, "w", newline="", encoding="utf-8") as f:
    writer = csv.DictWriter(f, fieldnames=fieldnames)
    writer.writeheader()
    writer.writerows(rows)

print(f"Amostras coletadas: {len(rows)}")
print(f"CSV salvo em: {out_path}")
