"""
plot.py – EXP03: Modificações concorrentes (LWW)

Gera gráfico de linhas acumuladas de convergências e divergências ao longo
das repetições.
"""
import sys
import csv
from pathlib import Path

sys.path.insert(0, '/app')
import matplotlib.pyplot as plt
from style import apply_style, save_fig, CORES

RESULTS = Path('results')
IN_CSV = RESULTS / 'concorrencia.csv'
OUT_BASE = str(RESULTS / 'concorrencia_lww')


def main():
    if not IN_CSV.exists():
        print(f'ERRO: {IN_CSV} não encontrado. Execute collect.py primeiro.')
        sys.exit(1)

    reps = []
    converged_flags = []
    with open(IN_CSV, newline='', encoding='utf-8') as f:
        reader = csv.DictReader(f)
        for row in reader:
            reps.append(int(row['rep']))
            converged_flags.append(int(row['converged']))

    if not reps:
        print('ERRO: concorrencia.csv está vazio.')
        sys.exit(1)

    # Acumulados
    acc_conv = []
    acc_div = []
    c = 0
    d = 0
    for flag in converged_flags:
        if flag == 1:
            c += 1
        else:
            d += 1
        acc_conv.append(c)
        acc_div.append(d)

    total = len(reps)
    n_conv = acc_conv[-1]
    n_div = acc_div[-1]
    pct = n_conv / total * 100 if total > 0 else 0.0

    apply_style()
    fig, ax = plt.subplots(figsize=(9, 5))

    ax.plot(reps, acc_conv, 'o-', color=CORES['verde'],
            linewidth=2, markersize=4, label='Convergências acumuladas')
    ax.plot(reps, acc_div, 's-', color=CORES['vermelho'],
            linewidth=2, markersize=4, label='Divergências acumuladas')

    ax.set_xlabel('Repetição')
    ax.set_ylabel('Contagem acumulada')
    # ax.set_title('EXP03 – Modificações Concorrentes (LWW): Convergência Acumulada')
    ax.legend(loc='upper left')

    # Texto informativo no canto superior direito
    texto = f'Taxa de convergência: {n_conv}/{total} ({pct:.1f}%)'
    ax.text(0.98, 0.05, texto,
            transform=ax.transAxes,
            fontsize=10, va='bottom', ha='right',
            bbox=dict(boxstyle='round,pad=0.3', facecolor='white', alpha=0.8))

    ax.set_xlim(left=0)
    ax.set_ylim(bottom=0)

    save_fig(fig, OUT_BASE)
    plt.close(fig)
    print('EXP03: gráfico gerado com sucesso.')


if __name__ == '__main__':
    main()
