"""
style.py – estilo padronizado para gráficos dos experimentos Syncli.

Uso:
    from style import apply_style, save_fig, scatter_over_median, CORES
    apply_style()
    fig, ax = plt.subplots()
    ...
    save_fig(fig, 'results/meu_grafico')
"""
import matplotlib.pyplot as plt
import matplotlib as mpl
import numpy as np

CORES = {
    'azul':     '#1f77b4',
    'laranja':  '#ff7f0e',
    'verde':    '#2ca02c',
    'vermelho': '#d62728',
    'roxo':     '#9467bd',
    'marrom':   '#8c564b',
    'rosa':     '#e377c2',
    'cinza':    '#7f7f7f',
}
PALETA = list(CORES.values())


def apply_style():
    """Aplica rcParams padrão. Chame uma vez antes de criar figuras."""
    mpl.rcParams.update({
        'figure.figsize':     (8, 5),
        'font.family':        'DejaVu Sans',
        'font.size':          11,
        'axes.titlesize':     12,
        'axes.titleweight':   'bold',
        'axes.labelsize':     11,
        'xtick.labelsize':    10,
        'ytick.labelsize':    10,
        'legend.fontsize':    10,
        'axes.grid':          True,
        'grid.alpha':         0.3,
        'grid.linestyle':     '--',
        'figure.dpi':         100,
        'savefig.dpi':        300,
        'savefig.bbox':       'tight',
        'axes.prop_cycle':    mpl.cycler(color=PALETA),
        'axes.spines.top':    False,
        'axes.spines.right':  False,
    })


def save_fig(fig: plt.Figure, basepath: str):
    """Salva figura em PNG (300 dpi) e PDF vetorial."""
    fig.savefig(f'{basepath}.png', dpi=300, bbox_inches='tight')
    fig.savefig(f'{basepath}.pdf', bbox_inches='tight')
    print(f'  → {basepath}.png / .pdf')


def scatter_over_median(ax, x_positions, y_groups, color=None, label_median='Mediana'):
    """
    Plota pontos individuais (alpha=0.4) sobre a linha de medianas.

    Parâmetros
    ----------
    ax           : eixo matplotlib
    x_positions  : lista de posições x (numéricas)
    y_groups     : lista de arrays/listas de valores y correspondentes a cada x
    color        : cor única para pontos e mediana (padrão: azul)
    label_median : rótulo da linha de mediana na legenda
    """
    c = color or CORES['azul']
    medians = []
    for x, ys in zip(x_positions, y_groups):
        ys = np.asarray(ys, dtype=float)
        ax.scatter([x] * len(ys), ys, alpha=0.4, s=15, color=c, zorder=5)
        medians.append(np.median(ys))
    ax.plot(x_positions, medians, 'o-', color=c, linewidth=1.8,
            label=label_median, zorder=10)
    return medians


def nota_dispersao(ax, x=0.01, y=0.97):
    """Adiciona nota de rodapé sobre pontos individuais (N pequeno)."""
    # ax.text(x, y,
    #         'Pontos individuais mostrados; dispersão apenas indicativa (N pequeno).',
    #         transform=ax.transAxes, fontsize=8, va='top', ha='left', color='gray')
