# The psychometrics worker: plain Python, one dependency, non-root.
FROM python:3.13-slim
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY ctt.py worker.py ./
USER 65532:65532
CMD ["python", "worker.py"]
