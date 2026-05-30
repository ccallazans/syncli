"""
plot.py – EXP09: Overhead de recursos (CPU e memória)
Gera dois gráficos:
  1. recursos_cpu_mem   – barras agrupadas de CPU% e MEM MB para condições
                          repouso-pequeno e atividade (dois eixos Y)
  2. recursos_escala_mem – curva de MEM média (MB) em repouso vs N arquivos
"""
import numpy as np
import pandas as pd
import matplotlib.pyplot as plt
from pathlib import Path

from style import apply_style, save_fig, CORES

apply_style()

RESULTS = Path("results")
df = pd.read_csv(RESULTS / "recursos.csv")

if df.empty:
    raise SystemExit("ERRO: recursos.csv vazio.")

# --------------------------------------------------------------------------
# Gráfico 1 – Barras agrupadas CPU% e MEM MB (condições 1 e 2)
# Inclui apenas condições operacionais (não de escala)
# --------------------------------------------------------------------------
cond_plot1 = ["repouso_pequeno", "atividade"]
df1 = df[df["condicao"].isin(cond_plot1)].copy()

# Garante ordem: repouso_pequeno primeiro, depois atividade
df1["_order"] = df1["condicao"].map({c: i for i, c in enumerate(cond_plot1)})
df1 = df1.sort_values("_order").reset_index(drop=True)

labels_cond = {
    "repouso_pequeno": "Repouso\n(10 arquivos)",
    "atividade":       "Atividade\n(1 arq/500ms)",
}

if not df1.empty:
    x = np.arange(len(df1))
    bar_w = 0.5
    cores_cond = [CORES["azul"], CORES["laranja"]]
    labels_x = [labels_cond.get(c, c) for c in df1["condicao"]]

    fig1, (ax_cpu, ax_mem) = plt.subplots(1, 2, figsize=(10, 5))

    for ax, col_mean, col_peak, ylabel, title, fmt_peak in [
        (ax_cpu, "cpu_mean_pct", "cpu_peak_pct", "CPU (%)",      "CPU",      "{:.2f}%"),
        (ax_mem, "mem_mean_mb",  "mem_peak_mb",  "Memória (MB)", "Memória",  "{:.1f} MB"),
    ]:
        ax.bar(x, df1[col_mean], bar_w, color=cores_cond, alpha=0.85)

        for i, (_, peak_val) in enumerate(zip(df1[col_mean], df1[col_peak])):
            ax.hlines(peak_val, i - bar_w / 2, i + bar_w / 2,
                      colors="black", linewidths=1.2, linestyles="--")
            ax.annotate(
                "pico: " + fmt_peak.format(peak_val),
                xy=(i, peak_val), xytext=(0, 4),
                textcoords="offset points", ha="center", fontsize=8,
            )

        ax.set_xticks(x)
        ax.set_xticklabels(labels_x, fontsize=10)
        ax.set_ylabel(ylabel)
        ax.set_title(title)

    # fig1.suptitle("Overhead de Recursos – Repouso vs Atividade", fontweight="bold")
    fig1.tight_layout()
    save_fig(fig1, str(RESULTS / "recursos_cpu_mem"))
    plt.close(fig1)
else:
    print("AVISO: sem dados de repouso_pequeno/atividade para o gráfico 1.")

# --------------------------------------------------------------------------
# Gráfico 2 – Curva de MEM média vs N arquivos (condições de escala)
# Inclui repouso_pequeno (n=10) + repouso_100/1000/5000
# --------------------------------------------------------------------------
escala_conds = df[df["n_files"] > 0].copy()
escala_conds = escala_conds.sort_values("n_files").reset_index(drop=True)

if len(escala_conds) >= 2:
    fig2, ax2 = plt.subplots()

    ax2.plot(
        escala_conds["n_files"],
        escala_conds["mem_mean_mb"],
        "o-",
        color=CORES["azul"],
        linewidth=1.8,
        markersize=7,
        label="Memória média (MB)",
        zorder=10,
    )

    # Pico como área sombreada
    ax2.fill_between(
        escala_conds["n_files"],
        escala_conds["mem_mean_mb"],
        escala_conds["mem_peak_mb"],
        alpha=0.2,
        color=CORES["azul"],
        label="Faixa até pico",
    )

    ax2.set_xlabel("Número de arquivos monitorados")
    ax2.set_ylabel("Memória (MB)")
    # ax2.set_title("Overhead de Memória em Repouso vs N Arquivos Monitorados")

    # Anotações nos pontos
    for _, row in escala_conds.iterrows():
        ax2.annotate(
            f"{row['mem_mean_mb']:.1f} MB",
            xy=(row["n_files"], row["mem_mean_mb"]),
            xytext=(5, 5),
            textcoords="offset points",
            fontsize=8,
            color=CORES["azul"],
        )

    ax2.legend(loc="upper left")
    fig2.tight_layout()
    save_fig(fig2, str(RESULTS / "recursos_escala_mem"))
    plt.close(fig2)
else:
    print("AVISO: dados insuficientes para o gráfico de escala de memória (necessário >= 2 pontos).")

print("Gráficos gerados: recursos_cpu_mem, recursos_escala_mem")
