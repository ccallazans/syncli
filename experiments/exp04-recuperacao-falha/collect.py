"""
collect.py – EXP04: Recuperação após falha de par

Lê results/timeline.json e os JSONLs.
Calcula deltas em segundos e salva results/recuperacao.csv.
"""
import csv
import json
import sys
from pathlib import Path

sys.path.insert(0, '/app')
from parse_explog import load_jsonl, filter_events

RESULTS = Path('results')
TIMELINE_JSON = RESULTS / 'timeline.json'
OUT_CSV = RESULTS / 'recuperacao.csv'
LOG_A = RESULTS / 'nodeA.jsonl'
LOG_B = RESULTS / 'nodeB.jsonl'


def main():
    if not TIMELINE_JSON.exists():
        print(f'ERRO: {TIMELINE_JSON} não encontrado. Execute run.sh primeiro.')
        sys.exit(1)

    with open(TIMELINE_JSON, encoding='utf-8') as f:
        tl = json.load(f)

    ts_stop        = int(tl['ts_stop'])
    ts_removed     = int(tl['ts_removed'])
    ts_start_again = int(tl['ts_start_again'])
    ts_peer_disc   = int(tl['ts_peer_disc'])
    ts_snapshot_end = int(tl['ts_snapshot_end'])
    offline_write_ok = int(tl.get('offline_write_ok', 0))

    # Deltas em segundos
    queda_remocao_s      = (ts_removed - ts_stop) / 1e9
    parada_redescoberta_s = (ts_peer_disc - ts_start_again) / 1e9
    sync_duracao_s       = (ts_snapshot_end - ts_peer_disc) / 1e9
    total_indisponivel_s  = (ts_peer_disc - ts_stop) / 1e9

    # Enriquece com contagens de eventos relevantes dos JSONLs
    df_A = load_jsonl(str(LOG_A))
    df_B = load_jsonl(str(LOG_B))

    n_peer_removed_A = 0
    n_peer_disc_A    = 0
    n_snapshot_end_B = 0

    if not df_A.empty:
        n_peer_removed_A = len(filter_events(df_A, 'peer_removed'))
        n_peer_disc_A    = len(filter_events(df_A, 'peer_discovered'))

    if not df_B.empty:
        n_snapshot_end_B = len(filter_events(df_B, 'snapshot_end'))

    row = {
        'ts_stop':               ts_stop,
        'ts_removed':            ts_removed,
        'ts_start_again':        ts_start_again,
        'ts_peer_disc':          ts_peer_disc,
        'ts_snapshot_end':       ts_snapshot_end,
        'queda_remocao_s':       round(queda_remocao_s, 3),
        'parada_redescoberta_s': round(parada_redescoberta_s, 3),
        'sync_duracao_s':        round(sync_duracao_s, 3),
        'total_indisponivel_s':  round(total_indisponivel_s, 3),
        'offline_write_ok':      offline_write_ok,
        'n_peer_removed_A':      n_peer_removed_A,
        'n_peer_disc_A':         n_peer_disc_A,
        'n_snapshot_end_B':      n_snapshot_end_B,
    }

    fieldnames = list(row.keys())
    with open(OUT_CSV, 'w', newline='', encoding='utf-8') as f:
        writer = csv.DictWriter(f, fieldnames=fieldnames)
        writer.writeheader()
        writer.writerow(row)

    print(f'Queda → remoção:          {queda_remocao_s:.3f}s')
    print(f'Parada → redescoberta:    {parada_redescoberta_s:.3f}s')
    print(f'Redescoberta → fim sync:  {sync_duracao_s:.3f}s')
    print(f'Total indisponível:       {total_indisponivel_s:.3f}s')
    print(f'offline_write.txt chegou: {"SIM" if offline_write_ok else "NÃO"}')
    print(f'Salvo em: {OUT_CSV}')


if __name__ == '__main__':
    main()
