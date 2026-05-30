"""
plot.py – EXP07: Snapshot inicial
Gera gráfico de tempo de snapshot (ms) vs volume total (MB):
pontos individuais por rep + linha de mediana por volume.
Salvo em results/snapshot_duracao.{png,pdf}.
"""
import numpy as np
import pandas as pd
import matplotlib.pyplot as plt
from pathlib import Path

from style import apply_style, save_fig, scatter_over_median, nota_dispersao, CORES

apply_style()

RESULTS = Path("results")
df = pd.read_csv(RESULTS / "snapshot.csv")

if df.empty:
    raise SystemExit("ERRO: snapshot.csv vazio.")

# --------------------------------------------------------------------------
# Preparação
# --------------------------------------------------------------------------
volumes_ordenados = sorted(df["volume_mb"].unique())

x_positions = volumes_ordenados
y_groups    = [
    df[df["volume_mb"] == v]["duration_ms"].dropna().values
    for v in volumes_ordenados
]

# --------------------------------------------------------------------------
# Gráfico: tempo de snapshot vs volume
# --------------------------------------------------------------------------
fig, ax = plt.subplots()

medians = scatter_over_median(
    ax, x_positions, y_groups,
    color=CORES["azul"],
    label_median="Mediana"
)

# Anotação de cada ponto de mediana
for x, med in zip(x_positions, medians):
    if med >= 1000:
        label = f"{med / 1000:.1f} s"
    else:
        label = f"{med:.0f} ms"
    ax.annotate(
        label,
        xy=(x, med),
        xytext=(0, 8),
        textcoords="offset points",
        ha="center",
        fontsize=9,
        color=CORES["azul"],
    )

ax.set_xlabel("Volume total (MB)")
ax.set_ylabel("Duração do snapshot (ms)")
# ax.set_title("Snapshot Inicial – Duração vs Volume de Dados")

ax.set_xticks(volumes_ordenados)
ax.set_xticklabels([f"{v} MB" for v in volumes_ordenados])

nota_dispersao(ax)
ax.legend()
fig.tight_layout()
save_fig(fig, str(RESULTS / "snapshot_duracao"))
plt.close(fig)

print("Gráfico gerado: snapshot_duracao")
