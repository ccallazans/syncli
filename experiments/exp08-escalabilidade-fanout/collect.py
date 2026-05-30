"""
collect.py – EXP08: Escalabilidade do fan-out
Lê o JSONL de nodeA e calcula, para cada arquivo enviado:
  - last_ack_ms: tempo até o último ACK dentre todos os pares
  - ack_p50_ms / ack_p95_ms: percentis dos tempos de ACK individuais por par
Salva em results/fanout.csv.
"""
import re
import csv
import numpy as np
from pathlib import Path

from parse_explog import load_jsonl, filter_events

RESULTS = Path("results")
# Padrão: test_n<N>_r<rep>.bin  (ex.: test_n4_r003.bin)
PATTERN = re.compile(r"test_n(\d+)_r(\d+)\.bin$")

# --------------------------------------------------------------------------
# Carrega logs de nodeA (um por configuração de N: nodeA_n2.jsonl, nodeA_n3.jsonl …)
# Fallback: lê nodeA.jsonl caso os arquivos por N não existam
# --------------------------------------------------------------------------
import pandas as pd

per_n_logs = sorted(RESULTS.glob("fanout_n*.jsonl"))
if per_n_logs:
    frames = [load_jsonl(str(p)) for p in per_n_logs]
    dfA = pd.concat([f for f in frames if not f.empty], ignore_index=True)
else:
    dfA = load_jsonl(str(RESULTS / "nodeA.jsonl"))

if dfA.empty:
    raise SystemExit("ERRO: nenhum nodeA_n*.jsonl (ou nodeA.jsonl) encontrado com dados.")

send_starts = filter_events(dfA, "send_start")
ack_recvs   = filter_events(dfA, "ack_recv")

if send_starts.empty:
    raise SystemExit("ERRO: nenhum evento send_start encontrado em nodeA.jsonl.")

# --------------------------------------------------------------------------
# Coleta por send_start
# --------------------------------------------------------------------------
rows = []

for _, ss in send_starts.iterrows():
    path = ss.get("path", "")
    m = PATTERN.search(str(path))
    if not m:
        continue

    n_nodes = int(m.group(1))   # número total de nós (escritor + pares)
    rep     = int(m.group(2))
    n_peers = n_nodes - 1
    ts_send = int(ss["ts_ns"])

    # Todos os ack_recv para esse path após send_start
    cand_acks = ack_recvs[
        (ack_recvs["path"] == path) &
        (ack_recvs["ts_ns"] > ts_send)
    ]

    if cand_acks.empty:
        continue

    # Tempo de ACK por par (ms)
    ack_times_ms = [
        (int(row["ts_ns"]) - ts_send) / 1e6
        for _, row in cand_acks.iterrows()
    ]

    # Caso haja mais acks do que pares esperados (re-envios), usa os primeiros N-1
    ack_times_ms = sorted(ack_times_ms)[:n_peers] if len(ack_times_ms) > n_peers else ack_times_ms

    if not ack_times_ms:
        continue

    last_ack_ms = float(np.max(ack_times_ms))
    ack_p50_ms  = float(np.percentile(ack_times_ms, 50))
    ack_p95_ms  = float(np.percentile(ack_times_ms, 95))

    rows.append({
        "n_peers":     n_peers,
        "n_nodes":     n_nodes,
        "rep":         rep,
        "path":        path,
        "last_ack_ms": round(last_ack_ms, 3),
        "ack_p50_ms":  round(ack_p50_ms,  3),
        "ack_p95_ms":  round(ack_p95_ms,  3),
    })

if not rows:
    raise SystemExit("ERRO: nenhuma amostra encontrada. Verifique os JSONLs.")

# --------------------------------------------------------------------------
# Salva CSV
# --------------------------------------------------------------------------
out_path = RESULTS / "fanout.csv"
fieldnames = ["n_peers", "n_nodes", "rep", "path",
              "last_ack_ms", "ack_p50_ms", "ack_p95_ms"]

with open(out_path, "w", newline="", encoding="utf-8") as f:
    writer = csv.DictWriter(f, fieldnames=fieldnames)
    writer.writeheader()
    writer.writerows(rows)

print(f"Amostras coletadas: {len(rows)}")
print(f"Configurações de N encontradas: {sorted(set(r['n_nodes'] for r in rows))}")
print(f"CSV salvo em: {out_path}")
