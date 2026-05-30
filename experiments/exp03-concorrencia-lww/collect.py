"""
collect.py – EXP03: Modificações concorrentes (LWW)

Lê results/raw.csv e os JSONLs.
Para cada repetição, extrai contagem de apply_rejected com path=concurrent.txt.
Salva results/concorrencia.csv.
"""
import csv
import sys
from pathlib import Path

sys.path.insert(0, '/app')
from parse_explog import load_jsonl, filter_events

RESULTS = Path('results')
RAW_CSV = RESULTS / 'raw.csv'
OUT_CSV = RESULTS / 'concorrencia.csv'
LOG_A = RESULTS / 'nodeA.jsonl'
LOG_B = RESULTS / 'nodeB.jsonl'


def count_rejected(df, path_name, start_ts_ns, end_ts_ns):
    """Conta apply_rejected para o path dado dentro do intervalo [start_ts_ns, end_ts_ns]."""
    if df.empty:
        return 0, ''
    rejected = filter_events(df, 'apply_rejected')
    if rejected.empty:
        return 0, ''
    if 'path' in rejected.columns:
        rejected = rejected[rejected['path'].str.endswith(path_name, na=False)]
    if 'ts_ns' in rejected.columns:
        rejected = rejected[
            (rejected['ts_ns'] >= start_ts_ns) & (rejected['ts_ns'] <= end_ts_ns)
        ]
    if rejected.empty:
        return 0, ''
    count = len(rejected)
    reasons = ''
    if 'reason' in rejected.columns:
        reasons = ','.join(sorted(rejected['reason'].dropna().unique().tolist()))
    return count, reasons


def main():
    if not RAW_CSV.exists():
        print(f'ERRO: {RAW_CSV} não encontrado. Execute run.sh primeiro.')
        sys.exit(1)

    df_A = load_jsonl(str(LOG_A))
    df_B = load_jsonl(str(LOG_B))

    rows_in = []
    with open(RAW_CSV, newline='', encoding='utf-8') as f:
        reader = csv.DictReader(f)
        for row in reader:
            rows_in.append(row)

    if not rows_in:
        print('ERRO: raw.csv está vazio.')
        sys.exit(1)

    print(f'Processando {len(rows_in)} repetições...')

    rows_out = []
    for row in rows_in:
        rep = int(row['rep'])
        hash_A = row['hash_A']
        hash_B = row['hash_B']
        converged = int(row['converged'])
        start_ts_ns = int(row['start_ts_ns'])
        end_ts_ns = int(row['end_ts_ns'])

        rej_A, reasons_A = count_rejected(df_A, 'concurrent.txt', start_ts_ns, end_ts_ns)
        rej_B, reasons_B = count_rejected(df_B, 'concurrent.txt', start_ts_ns, end_ts_ns)

        # Combina razões de ambos os nós (distintas)
        all_reasons = set()
        if reasons_A:
            all_reasons.update(reasons_A.split(','))
        if reasons_B:
            all_reasons.update(reasons_B.split(','))
        lww_reasons = ','.join(sorted(all_reasons)) if all_reasons else ''

        rows_out.append({
            'rep':        rep,
            'hash_A':     hash_A,
            'hash_B':     hash_B,
            'converged':  converged,
            'rejected_A': rej_A,
            'rejected_B': rej_B,
            'lww_reasons': lww_reasons,
        })

    fieldnames = ['rep', 'hash_A', 'hash_B', 'converged', 'rejected_A', 'rejected_B', 'lww_reasons']
    with open(OUT_CSV, 'w', newline='', encoding='utf-8') as f:
        writer = csv.DictWriter(f, fieldnames=fieldnames)
        writer.writeheader()
        writer.writerows(rows_out)

    total = len(rows_out)
    conv = sum(r['converged'] for r in rows_out)
    pct = conv / total * 100 if total > 0 else 0.0
    print(f'Taxa de convergência: {conv}/{total} ({pct:.1f}%)')
    print(f'Salvo em: {OUT_CSV}')


if __name__ == '__main__':
    main()
