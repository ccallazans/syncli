"""
plot.py – EXP04: Recuperação após falha de par

Gera gráfico de linha do tempo com marcadores verticais coloridos para
cada evento da sequência de falha e recuperação.
"""
import csv
import sys
from pathlib import Path

sys.path.insert(0, '/app')
import matplotlib.pyplot as plt
import matplotlib.patches as mpatches
from style import apply_style, save_fig, CORES

RESULTS = Path('results')
IN_CSV = RESULTS / 'recuperacao.csv'
OUT_BASE = str(RESULTS / 'timeline')


def main():
    if not IN_CSV.exists():
        print(f'ERRO: {IN_CSV} não encontrado. Execute collect.py primeiro.')
        sys.exit(1)

    with open(IN_CSV, newline='', encoding='utf-8') as f:
        reader = csv.DictReader(f)
        row = next(reader, None)

    if row is None:
        print('ERRO: recuperacao.csv está vazio.')
        sys.exit(1)

    ts_stop        = int(row['ts_stop'])
    ts_removed     = int(row['ts_removed'])
    ts_start_again = int(row['ts_start_again'])
    ts_peer_disc   = int(row['ts_peer_disc'])
    ts_snapshot_end = int(row['ts_snapshot_end'])

    # Converte para segundos relativos ao ts_stop = 0
    def rel(ts):
        return (int(ts) - ts_stop) / 1e9

    # Ordem cronológica real dos eventos
    eventos = [
        (0.0,                    'Queda\n(nodeB parado)',           CORES['vermelho']),
        (rel(ts_removed),        'Remoção\n(peer_removed)',         CORES['laranja']),
        (rel(ts_start_again),    'Volta\n(nodeB reiniciado)',       CORES['verde']),
        (rel(ts_snapshot_end),   'Fim snapshot\n(snapshot_end)',    CORES['roxo']),
        (rel(ts_peer_disc),      'Redescoberta\n(peer_discovered)', CORES['azul']),
    ]

    apply_style()
    fig, ax = plt.subplots(figsize=(12, 5))
    fig.subplots_adjust(top=0.92, bottom=0.22, left=0.04, right=0.97)

    YLIM_MIN, YLIM_MAX = -0.15, 1.0
    YSPAN = YLIM_MAX - YLIM_MIN
    baseline_frac = abs(YLIM_MIN) / YSPAN

    x_max = max(e[0] for e in eventos) * 1.10
    x_min = min(e[0] for e in eventos) - 1.0

    ax.axhline(y=0, color=CORES['cinza'], linewidth=1.5, zorder=1)

    # Alturas escalonadas para evitar sobreposição de labels em eventos próximos
    label_heights = [0.45, 0.82, 0.58, 0.73, 0.92]

    for i, (t, label, cor) in enumerate(eventos):
        h = label_heights[i]
        ax.axvline(x=t, color=cor, linewidth=2.0, linestyle='--',
                   ymin=baseline_frac, ymax=h - 0.03,
                   zorder=2, alpha=0.85)
        ax.scatter([t], [0], color=cor, s=80, zorder=5)
        ax.text(t, h, f'{label}\n{t:+.1f}s',
                transform=ax.get_xaxis_transform(),
                ha='center', va='bottom',
                fontsize=8.5, color=cor,
                linespacing=1.3, clip_on=False)

    patches = [
        mpatches.Patch(color=CORES['vermelho'], label='Queda'),
        mpatches.Patch(color=CORES['laranja'],  label='Remoção'),
        mpatches.Patch(color=CORES['verde'],    label='Volta'),
        mpatches.Patch(color=CORES['roxo'],     label='Fim snapshot'),
        mpatches.Patch(color=CORES['azul'],     label='Redescoberta'),
    ]
    ax.legend(handles=patches,
              loc='upper center',
              bbox_to_anchor=(0.5, -0.14),
              ncol=5, fontsize=9)

    ax.set_xlabel('Tempo relativo à queda (segundos)')

    ax.yaxis.set_visible(False)
    ax.spines['left'].set_visible(False)

    ax.set_xlim(x_min, x_max)
    ax.set_ylim(YLIM_MIN, YLIM_MAX)

    save_fig(fig, OUT_BASE)
    plt.close(fig)
    print('EXP04: gráfico gerado com sucesso.')


if __name__ == '__main__':
    main()
