"""
parse_explog.py – utilitários compartilhados para leitura de logs JSONL do explog.

Cada linha do arquivo JSONL contém campos obrigatórios:
  ts_ns  : int  – CLOCK_REALTIME em nanosegundos (comparável entre nós no mesmo host)
  node   : str  – hostname do container (ex.: nodeA, nodeB)
  event  : str  – tipo do evento

Campos opcionais (dependem do evento):
  path, op, bytes, bytes_wire, version, peer, reason, files, etc.
"""
import json
import pandas as pd
from pathlib import Path


def load_jsonl(path: str) -> pd.DataFrame:
    """Lê arquivo JSONL e retorna DataFrame ordenado por ts_ns."""
    records = []
    p = Path(path)
    if not p.exists():
        return pd.DataFrame()
    with open(p, encoding='utf-8') as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                records.append(json.loads(line))
            except json.JSONDecodeError:
                continue
    if not records:
        return pd.DataFrame()
    df = pd.DataFrame(records)
    df['ts_ns'] = pd.to_numeric(df.get('ts_ns', 0), errors='coerce').fillna(0).astype('int64')
    df = df.sort_values('ts_ns').reset_index(drop=True)
    return df


def load_all_jsonl(results_dir: str) -> pd.DataFrame:
    """Carrega todos os *.jsonl de results_dir em um único DataFrame."""
    frames = []
    for p in Path(results_dir).glob('*.jsonl'):
        df = load_jsonl(str(p))
        if not df.empty:
            frames.append(df)
    if not frames:
        return pd.DataFrame()
    combined = pd.concat(frames, ignore_index=True)
    return combined.sort_values('ts_ns').reset_index(drop=True)


def filter_events(df: pd.DataFrame, event: str, **kwargs) -> pd.DataFrame:
    """Filtra DataFrame por event e campos adicionais (kwargs)."""
    if df.empty:
        return df
    mask = df['event'] == event
    for k, v in kwargs.items():
        if k in df.columns:
            mask &= df[k] == v
    return df[mask].copy()


def events_after(df: pd.DataFrame, ts_ns: int) -> pd.DataFrame:
    """Retorna eventos após ts_ns."""
    return df[df['ts_ns'] > ts_ns].copy()


def events_between(df: pd.DataFrame, ts_start: int, ts_end: int) -> pd.DataFrame:
    """Retorna eventos no intervalo [ts_start, ts_end]."""
    return df[(df['ts_ns'] >= ts_start) & (df['ts_ns'] <= ts_end)].copy()


def first_event(df: pd.DataFrame, event: str, after_ts: int = 0, **kwargs) -> dict | None:
    """Retorna o primeiro evento do tipo dado, após after_ts, ou None."""
    sub = filter_events(df, event, **kwargs)
    sub = sub[sub['ts_ns'] > after_ts]
    if sub.empty:
        return None
    return sub.iloc[0].to_dict()


def last_event(df: pd.DataFrame, event: str, before_ts: int | None = None, **kwargs) -> dict | None:
    """Retorna o último evento do tipo dado (antes de before_ts se fornecido), ou None."""
    sub = filter_events(df, event, **kwargs)
    if before_ts is not None:
        sub = sub[sub['ts_ns'] <= before_ts]
    if sub.empty:
        return None
    return sub.iloc[-1].to_dict()
