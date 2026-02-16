FROM python:3.12-slim

WORKDIR /app

COPY daily-summary/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY daily-summary/main.py .

CMD ["python", "main.py"]
