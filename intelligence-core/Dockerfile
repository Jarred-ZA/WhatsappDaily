FROM python:3.12-slim

WORKDIR /app

COPY intelligence-core/pyproject.toml .
COPY intelligence-core/src/ src/

RUN pip install --no-cache-dir .

CMD ["python", "-m", "src.main"]
