"""
plot.py – EXP08: Escalabilidade do fan-out
Gera dois gráficos:
  1. fanout_last_ack     – last_ack_ms vs número de nós (scatter + mediana)
  2. fanout_distribuicao – distribuição de tempos de ACK por par para N=5
     (ou o maior N disponível), usando box plot
"""
import pandas as pd
import matplotlib.pyplot as plt
from pathlib import Path

from style import apply_style, save_fig, scatter_over_median, CORES

apply_style()

RESULTS = Path("results")
df = pd.read_csv(RESULTS / "fanout.csv")

if df.empty:
    raise SystemExit("ERRO: fanout.csv vazio.")

# Ordena configurações de N disponíveis
nodes_avail = sorted(df["n_nodes"].unique())

# --------------------------------------------------------------------------
# Gráfico 1 – last_ack_ms vs número de nós
# --------------------------------------------------------------------------
fig1, ax1 = plt.subplots()

x_positions = nodes_avail
y_groups = [
    df[df["n_nodes"] == n]["last_ack_ms"].dropna().values
    for n in x_positions
]

medianas = scatter_over_median(
    ax1, x_positions, y_groups,
    color=CORES["azul"],
    label_median="Mediana"
)

ax1.set_xlabel("Número de nós (escritor + pares)")
ax1.set_ylabel("Tempo até último ACK (ms)")
ax1.set_title("Fan-out – Escalabilidade: Tempo até o Último ACK")
ax1.set_xticks(x_positions)
ax1.set_xticklabels([str(n) for n in x_positions])


ax1.legend()
fig1.tight_layout()
save_fig(fig1, str(RESULTS / "fanout_last_ack"))
plt.close(fig1)

# --------------------------------------------------------------------------
# Gráfico 2 – distribuição de tempos de ACK por par para N alvo
# --------------------------------------------------------------------------
# Usa N=5 se disponível, caso contrário o maior N no dataset
N_ALVO = 5 if 5 in nodes_avail else nodes_avail[-1]
df_n = df[df["n_nodes"] == N_ALVO].copy()

# Para o box plot precisamos dos tempos individuais de ACK por par.
# O collect.py salva last_ack_ms, ack_p50_ms e ack_p95_ms mas não os tempos
# individuais por peer. Usamos ack_p50 e ack_p95 para reconstruir uma
# visualização aproximada com os dados disponíveis.
# Porém, se quisermos um box plot por par seria necessário salvar os tempos
# individuais. Aqui plotamos a distribuição de last_ack_ms, ack_p50_ms e
# ack_p95_ms por repetição como box plots agrupados (uma caixa por métrica).

metrics = {
    "last_ack_ms": "Último ACK",
    "ack_p50_ms":  "Mediana (p50)",
    "ack_p95_ms":  "p95",
}

fig2, ax2 = plt.subplots()

data_boxes = [df_n[col].dropna().values for col in metrics]
labels_pt  = list(metrics.values())

bp = ax2.boxplot(
    data_boxes,
    tick_labels=labels_pt,
    patch_artist=True,
    medianprops=dict(color="black", linewidth=1.5),
    whiskerprops=dict(linewidth=1.2),
    capprops=dict(linewidth=1.2),
    flierprops=dict(marker="o", markersize=4, alpha=0.5),
)

cores_boxes = [CORES["vermelho"], CORES["azul"], CORES["laranja"]]
for patch, cor in zip(bp["boxes"], cores_boxes):
    patch.set_facecolor(cor)
    patch.set_alpha(0.6)

ax2.set_xlabel("Métrica de tempo de ACK")
ax2.set_ylabel("Tempo (ms)")
ax2.set_title(f"Fan-out – Distribuição de Tempos de ACK (N={N_ALVO} nós)")

ax2.text(
    0.01, 0.97,
    f"N={N_ALVO} nós; {len(df_n)} repetições; arquivo de 1 MB.",
    transform=ax2.transAxes,
    fontsize=8, va="top", ha="left", color="gray",
)

fig2.tight_layout()
save_fig(fig2, str(RESULTS / "fanout_distribuicao"))
plt.close(fig2)

print("Gráficos gerados: fanout_last_ack, fanout_distribuicao")
