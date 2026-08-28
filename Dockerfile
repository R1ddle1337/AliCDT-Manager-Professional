# Build the Vue console in the image so a fresh clone is enough to deploy.
FROM node:24-alpine AS frontend-build

WORKDIR /src/frontend
COPY frontend/package*.json ./
RUN npm install --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

FROM python:3.12-slim

WORKDIR /app
COPY backend/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY backend/ ./backend/
COPY --from=frontend-build /src/frontend/dist/ ./frontend/dist/
RUN mkdir -p /app/data

WORKDIR /app/backend
CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"]
