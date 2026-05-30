"""
collect.py – EXP02: Latência de detecção
Lê results/nodeA.jsonl e calcula, para cada debounce_out, o intervalo desde
o fs_event correspondente, separando a latência bruta do fsnotify do debounce
configurado de 200 ms.
"""
import re
import csv
from pathlib import Path

from parse_explog import load_jsonl, filter_events

RESULTS = Path("results")
PATTERN = re.compile(r"test_(\d+)\.bin$")
DEBOUNCE_CONFIGURADO_MS = 200.0

# --------------------------------------------------------------------------
# Carrega log de nodeA
# --------------------------------------------------------------------------
dfA = load_jsonl(str(RESULTS / "nodeA.jsonl"))

if dfA.empty:
    raise SystemExit("ERRO: nodeA.jsonl vazio ou ausente.")

# --------------------------------------------------------------------------
# Sub-DataFrames
# --------------------------------------------------------------------------
fs_events     = filter_events(dfA, "fs_event")
debounce_outs = filter_events(dfA, "debounce_out")

# --------------------------------------------------------------------------
# Coleta por debounce_out
# --------------------------------------------------------------------------
rows = []

for _, deb in debounce_outs.iterrows():
    path = deb.get("path", "")
    m = PATTERN.search(str(path))
    if not m:
        continue

    rep    = int(m.group(1))
    ts_deb = int(deb["ts_ns"])

    # fs_event: o mais recente ANTES deste debounce_out para o mesmo path
    cand_fs = fs_events[
        (fs_events["path"] == path) &
        (fs_events["ts_ns"] < ts_deb)
    ]
    if cand_fs.empty:
        continue
    fs_row = cand_fs.iloc[-1]
    ts_fs  = int(fs_row["ts_ns"])

    dt_fsnotify_ms = (ts_deb - ts_fs) / 1e6

    rows.append({
        "rep":                    rep,
        "path":                   path,
        "dt_fsnotify_ms":         round(dt_fsnotify_ms,        3),
        "debounce_configurado_ms": DEBOUNCE_CONFIGURADO_MS,
    })

if not rows:
    raise SystemExit("ERRO: nenhuma amostra encontrada nos logs. Verifique nodeA.jsonl.")

# --------------------------------------------------------------------------
# Salva CSV
# --------------------------------------------------------------------------
out_path = RESULTS / "deteccao.csv"
fieldnames = ["rep", "path", "dt_fsnotify_ms", "debounce_configurado_ms"]

with open(out_path, "w", newline="", encoding="utf-8") as f:
    writer = csv.DictWriter(f, fieldnames=fieldnames)
    writer.writeheader()
    writer.writerows(rows)

print(f"Amostras coletadas: {len(rows)}")
print(f"CSV salvo em: {out_path}")
