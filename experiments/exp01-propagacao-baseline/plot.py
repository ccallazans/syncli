"""
plot.py – EXP01: Propagação ponta-a-ponta
Gera dois gráficos:
  1. propagacao_total  – tempo total (ms) vs tamanho do arquivo (log X)
  2. propagacao_etapas – barras empilhadas das 4 etapas por tamanho
"""
import numpy as np
import pandas as pd
import matplotlib.pyplot as plt
import matplotlib.ticker as ticker
from pathlib import Path

from style import apply_style, save_fig, scatter_over_median, nota_dispersao, CORES

apply_style()

RESULTS = Path("results")
df = pd.read_csv(RESULTS / "propagacao.csv")

if df.empty:
    raise SystemExit("ERRO: propagacao.csv vazio.")

# --------------------------------------------------------------------------
# Preparação
# --------------------------------------------------------------------------
SIZES_ORDERED = sorted(df["size_bytes"].unique())

SIZE_LABELS = {
    1048576:    "1 MB",
    10485760:   "10 MB",
    104857600:  "100 MB",
    209715200:  "200 MB",
}

def size_label(b):
    if b in SIZE_LABELS:
        return SIZE_LABELS[b]
    if b >= 1_048_576:
        return f"{b // 1_048_576} MB"
    if b >= 1024:
        return f"{b // 1024} KB"
    return f"{b} B"

# --------------------------------------------------------------------------
# Gráfico 1 – Tempo total vs tamanho
# --------------------------------------------------------------------------
fig1, ax1 = plt.subplots()

x_positions = SIZES_ORDERED
y_groups    = [df[df["size_bytes"] == s]["total_ms"].dropna().values for s in x_positions]

scatter_over_median(ax1, x_positions, y_groups, color=CORES["azul"], label_median="Mediana")

ax1.set_xscale("log")
ax1.set_xlabel("Tamanho do arquivo")
ax1.set_ylabel("Tempo total de propagação (ms)")
# ax1.set_title("Propagação Ponta-a-Ponta – Tempo Total")

ax1.set_xticks(SIZES_ORDERED)
ax1.set_xticklabels([size_label(s) for s in SIZES_ORDERED], rotation=20, ha="right")
ax1.xaxis.set_minor_locator(ticker.NullLocator())

nota_dispersao(ax1)
ax1.legend()
fig1.tight_layout()
save_fig(fig1, str(RESULTS / "propagacao_total"))
plt.close(fig1)

# --------------------------------------------------------------------------
# Gráfico 2 – Barras empilhadas das 4 etapas (medianas)
# --------------------------------------------------------------------------
etapas   = ["detect_ms", "serial_ms", "tcp_ms", "ack_ms"]
labels_pt = {
    "detect_ms": "Detecção (debounce)",
    "serial_ms": "Serialização",
    "tcp_ms":    "TCP + Disco",
    "ack_ms":    "Retorno ACK",
}
cores_etapas = [CORES["azul"], CORES["laranja"], CORES["verde"], CORES["vermelho"]]

medianas = {e: [] for e in etapas}
for s in SIZES_ORDERED:
    sub = df[df["size_bytes"] == s]
    for e in etapas:
        medianas[e].append(float(np.median(sub[e].dropna().values)))

x_idx   = np.arange(len(SIZES_ORDERED))
bar_w   = 0.55
fig2, ax2 = plt.subplots()

bottom = np.zeros(len(SIZES_ORDERED))
for e, cor in zip(etapas, cores_etapas):
    vals = np.array(medianas[e])
    ax2.bar(x_idx, vals, bar_w, bottom=bottom, label=labels_pt[e], color=cor)
    bottom += vals

ax2.set_xticks(x_idx)
ax2.set_xticklabels([size_label(s) for s in SIZES_ORDERED], rotation=20, ha="right")
ax2.set_xlabel("Tamanho do arquivo")
ax2.set_ylabel("Latência mediana (ms)")
# ax2.set_title("Propagação Ponta-a-Ponta – Decomposição por Etapa")
ax2.legend(loc="upper left")
fig2.tight_layout()
save_fig(fig2, str(RESULTS / "propagacao_etapas"))
plt.close(fig2)

print("Gráficos gerados: propagacao_total, propagacao_etapas")
