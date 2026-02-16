FROM python:3.12-slim

WORKDIR /app

COPY intelligence-core/pyproject.toml .
RUN pip install --no-cache-dir .

COPY intelligence-core/src/ src/

CMD ["python", "-m", "src.main"]
