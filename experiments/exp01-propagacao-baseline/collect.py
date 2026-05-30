"""
collect.py – EXP01: Propagação ponta-a-ponta
Lê os logs JSONL de nodeA e nodeB e calcula as etapas de latência para cada
transferência de arquivo, salvando em results/propagacao.csv.
"""
import re
import csv
from pathlib import Path

from parse_explog import load_jsonl, filter_events

RESULTS = Path("results")
PATTERN = re.compile(r"test_(\d+)_(\d+)\.bin$")

# --------------------------------------------------------------------------
# Carrega logs
# --------------------------------------------------------------------------
dfA = load_jsonl(str(RESULTS / "nodeA.jsonl"))
dfB = load_jsonl(str(RESULTS / "nodeB.jsonl"))

if dfA.empty:
    raise SystemExit("ERRO: nodeA.jsonl vazio ou ausente.")
if dfB.empty:
    raise SystemExit("ERRO: nodeB.jsonl vazio ou ausente.")

# --------------------------------------------------------------------------
# Sub-DataFrames por tipo de evento
# --------------------------------------------------------------------------
fs_events      = filter_events(dfA, "fs_event")
debounce_outs  = filter_events(dfA, "debounce_out")
send_starts    = filter_events(dfA, "send_start")
ack_sends_B    = filter_events(dfB, "ack_send")
ack_recvs_A    = filter_events(dfA, "ack_recv")

# --------------------------------------------------------------------------
# Coleta por send_start
# --------------------------------------------------------------------------
rows = []

for _, ss in send_starts.iterrows():
    path = ss.get("path", "")
    m = PATTERN.search(str(path))
    if not m:
        continue

    size_bytes = int(m.group(1))
    rep        = int(m.group(2))
    ts_send    = int(ss["ts_ns"])
    bytes_wire = ss.get("bytes_wire", None)

    # fs_event: o mais recente antes do send_start para o mesmo path
    cand_fs = fs_events[
        (fs_events["path"] == path) &
        (fs_events["ts_ns"] < ts_send)
    ]
    if cand_fs.empty:
        continue
    fs_row = cand_fs.iloc[-1]
    ts_fs = int(fs_row["ts_ns"])

    # debounce_out: entre fs_event e send_start para o mesmo path
    cand_deb = debounce_outs[
        (debounce_outs["path"] == path) &
        (debounce_outs["ts_ns"] > ts_fs) &
        (debounce_outs["ts_ns"] <= ts_send)
    ]
    if cand_deb.empty:
        continue
    deb_row = cand_deb.iloc[-1]
    ts_deb = int(deb_row["ts_ns"])

    # ack_send em nodeB: após send_start para o mesmo path
    cand_ack_b = ack_sends_B[
        (ack_sends_B["path"] == path) &
        (ack_sends_B["ts_ns"] > ts_send)
    ]
    if cand_ack_b.empty:
        continue
    ack_b_row = cand_ack_b.iloc[0]
    ts_ack_b = int(ack_b_row["ts_ns"])

    # ack_recv em nodeA: após send_start para o mesmo path
    cand_ack_a = ack_recvs_A[
        (ack_recvs_A["path"] == path) &
        (ack_recvs_A["ts_ns"] > ts_send)
    ]
    if cand_ack_a.empty:
        continue
    ack_a_row = cand_ack_a.iloc[0]
    ts_ack_a = int(ack_a_row["ts_ns"])

    # Deltas em ms
    detect_ms = (ts_deb    - ts_fs)     / 1e6
    serial_ms = (ts_send   - ts_deb)    / 1e6
    tcp_ms    = (ts_ack_b  - ts_send)   / 1e6
    ack_ms    = (ts_ack_a  - ts_ack_b)  / 1e6
    total_ms  = (ts_ack_a  - ts_fs)     / 1e6

    rows.append({
        "path":       path,
        "size_bytes": size_bytes,
        "rep":        rep,
        "bytes_wire": bytes_wire,
        "detect_ms":  round(detect_ms, 3),
        "serial_ms":  round(serial_ms, 3),
        "tcp_ms":     round(tcp_ms,    3),
        "ack_ms":     round(ack_ms,    3),
        "total_ms":   round(total_ms,  3),
    })

if not rows:
    raise SystemExit("ERRO: nenhuma amostra encontrada nos logs. Verifique os JSONLs.")

# --------------------------------------------------------------------------
# Salva CSV
# --------------------------------------------------------------------------
out_path = RESULTS / "propagacao.csv"
fieldnames = ["path", "size_bytes", "rep", "bytes_wire",
              "detect_ms", "serial_ms", "tcp_ms", "ack_ms", "total_ms"]

with open(out_path, "w", newline="", encoding="utf-8") as f:
    writer = csv.DictWriter(f, fieldnames=fieldnames)
    writer.writeheader()
    writer.writerows(rows)

print(f"Amostras coletadas: {len(rows)}")
print(f"CSV salvo em: {out_path}")
