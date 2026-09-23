FROM node:24-alpine AS frontend
WORKDIR /app/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
ARG VITE_AUTH_URL=http://127.0.0.1:18082
ARG VITE_API_URL=http://127.0.0.1:8081
ENV VITE_AUTH_URL=$VITE_AUTH_URL VITE_API_URL=$VITE_API_URL
RUN npm run build

FROM golang:1.26.4-alpine3.23 AS backend
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/studyflow-api .

FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S app \
    && adduser -S app -G app
WORKDIR /app
COPY --from=backend /app/studyflow-api ./studyflow-api
COPY --from=backend /app/migrations ./migrations
COPY --from=frontend /app/frontend/dist ./frontend/dist
USER app
EXPOSE 8081
ENTRYPOINT ["./studyflow-api"]
