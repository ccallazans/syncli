# Imagem Python para coleta (CSV) e geração de gráficos.
# parse_explog.py e style.py ficam em /app e são importados via PYTHONPATH=/app.

FROM python:3.12-slim
WORKDIR /app
RUN pip install --no-cache-dir matplotlib pandas numpy
COPY parse_explog.py style.py ./
