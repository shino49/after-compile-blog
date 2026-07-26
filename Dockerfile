FROM node:22-alpine AS frontend
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build
FROM golang:1.24-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /after-compile ./cmd/server
FROM alpine:3.22
RUN apk add --no-cache ca-certificates && addgroup -S app && adduser -S -G app app && mkdir /data /app && chown app:app /data /app
WORKDIR /app
COPY --from=backend /after-compile ./after-compile
COPY --from=frontend /src/web/dist ./web
USER app
ENV PORT=8080 DATABASE_PATH=/data/blog.db WEB_DIST=/app/web COOKIE_SECURE=false
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 CMD wget -qO- http://127.0.0.1:8080/api/health || exit 1
ENTRYPOINT ["./after-compile"]
