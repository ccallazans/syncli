"""
plot.py – EXP02: Latência de detecção
Gera histograma de dt_fsnotify_ms com linha vertical no debounce configurado (200 ms).
"""
import numpy as np
import pandas as pd
import matplotlib.pyplot as plt
from pathlib import Path

from style import apply_style, save_fig, CORES

apply_style()

RESULTS = Path("results")
df = pd.read_csv(RESULTS / "deteccao.csv")

if df.empty:
    raise SystemExit("ERRO: deteccao.csv vazio.")

dt_values       = df["dt_fsnotify_ms"].dropna().values
debounce_cfg_ms = float(df["debounce_configurado_ms"].iloc[0])

# --------------------------------------------------------------------------
# Bins (regra de Sturges com mínimo de 5)
# --------------------------------------------------------------------------
n_bins = max(5, int(np.ceil(np.log2(len(dt_values)) + 1)))
counts, edges = np.histogram(dt_values, bins=n_bins)
widths  = np.diff(edges)
centers = edges[:-1] + widths / 2

mediana = float(np.median(dt_values))

# --------------------------------------------------------------------------
# Gráfico – histograma + linhas de referência
# --------------------------------------------------------------------------
fig, ax = plt.subplots()

ax.bar(
    centers, counts, widths,
    color=CORES["azul"],
    alpha=0.75,
    label="Latência fsnotify medida",
    zorder=3,
)

ax.axvline(
    x=debounce_cfg_ms,
    color=CORES["vermelho"],
    linewidth=2,
    linestyle="--",
    label=f"Debounce configurado ({debounce_cfg_ms:.0f} ms)",
    zorder=5,
)

ax.axvline(
    x=mediana,
    color=CORES["laranja"],
    linewidth=1.5,
    linestyle=":",
    label=f"Mediana medida ({mediana:.1f} ms)",
    zorder=4,
)

ax.set_xlabel("Latência de detecção (ms)")
ax.set_ylabel("Frequência")
# ax.set_title("Latência de Detecção: fsnotify vs Debounce Configurado")
ax.legend()
fig.tight_layout()

save_fig(fig, str(RESULTS / "latencia_deteccao"))
plt.close(fig)

print("Gráfico gerado: latencia_deteccao")
