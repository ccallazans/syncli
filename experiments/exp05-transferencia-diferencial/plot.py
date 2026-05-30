"""
plot.py – EXP05: Transferência diferencial
Gera gráfico de barras com 4 colunas:
  1. Integral (ref) – opWrite baseline
  2. Delta Caso-a   – modificar bloco no meio (~64 KB)
  3. Delta Caso-b   – inserir no meio (~5 MB)
  4. Delta Caso-c   – inserir no início (fallback opWrite ~10 MB)
Salvo em results/diferencial.{png,pdf}.
"""
import numpy as np
import pandas as pd
import matplotlib.pyplot as plt
import matplotlib.ticker as ticker
from pathlib import Path

from style import apply_style, save_fig, CORES

apply_style()

RESULTS = Path("results")
df = pd.read_csv(RESULTS / "diferencial.csv")

if df.empty:
    raise SystemExit("ERRO: diferencial.csv vazio.")

df_a = df[df["caso"] == "a"]
df_b = df[df["caso"] == "b"]
df_c = df[df["caso"] == "c"]

# Referência: mediana dos bytes_wire_first (opWrite, arquivo 10 MB intacto)
ref_bytes = float(np.median(
    pd.concat([df_a["bytes_wire_first"],
               df_b["bytes_wire_first"],
               df_c["bytes_wire_first"]]).dropna()
))

vals_a = df_a["bytes_wire_second"].dropna().values.astype(float)
vals_b = df_b["bytes_wire_second"].dropna().values.astype(float)
vals_c = df_c["bytes_wire_second"].dropna().values.astype(float)

med_a = float(np.median(vals_a)) if len(vals_a) > 0 else 0.0
med_b = float(np.median(vals_b)) if len(vals_b) > 0 else 0.0
med_c = float(np.median(vals_c)) if len(vals_c) > 0 else 0.0

def reducao(med):
    if ref_bytes > 0:
        return (1.0 - med / ref_bytes) * 100.0
    return 0.0

red_a = reducao(med_a)
red_b = reducao(med_b)

# --------------------------------------------------------------------------
# Gráfico
# --------------------------------------------------------------------------
grupos       = ["Integral\n(ref)", "Delta Caso-a\n(modif. meio)", "Delta Caso-b\n(insert. meio)", "Delta Caso-c\n(insert. início)"]
medianas     = [ref_bytes, med_a, med_b, med_c]
cores_barras = [CORES["cinza"], CORES["verde"], CORES["azul"], CORES["laranja"]]
vals_por_col = [None, vals_a, vals_b, vals_c]

x_idx = np.arange(len(grupos))
bar_w = 0.5

fig, ax = plt.subplots()

bars = ax.bar(x_idx, medianas, bar_w, color=cores_barras, zorder=3)

# Anotações de redução para casos a e b
for idx, (red, med, cor) in enumerate([(red_a, med_a, CORES["verde"]),
                                        (red_b, med_b, CORES["azul"])], start=1):
    if red > 0:
        ax.annotate(
            f"−{red:.1f}%",
            xy=(x_idx[idx], med),
            xytext=(x_idx[idx], med + ref_bytes * 0.04),
            ha="center", va="bottom",
            fontsize=10, color=cor, fontweight="bold",
        )

# Pontos individuais sobre cada barra delta
for col_idx, (vals, cor) in enumerate(zip(vals_por_col[1:], cores_barras[1:]), start=1):
    if vals is not None and len(vals) > 0:
        ax.scatter(
            [x_idx[col_idx]] * len(vals), vals,
            color=cor, alpha=0.6, s=20, zorder=5,
        )

ax.set_xticks(x_idx)
ax.set_xticklabels(grupos)
ax.set_ylabel("Bytes transmitidos (mediana)")
# ax.set_title("Transferência Diferencial – Comparação de bytes_wire")

ax.yaxis.set_major_formatter(ticker.FuncFormatter(lambda x, _: f"{x / 1e6:.1f} MB"))

# ax.text(
#     0.01, 0.97,
#     "Pontos individuais mostrados sobre barras de mediana.",
#     transform=ax.transAxes, fontsize=8, va="top", ha="left", color="gray",
# )

fig.tight_layout()
save_fig(fig, str(RESULTS / "diferencial"))
plt.close(fig)

print("Gráfico gerado: diferencial")
