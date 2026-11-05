FROM oven/bun:1.3.14-alpine AS frontend
WORKDIR /src/web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ ./
RUN bun run typecheck && bun run lint && bun run test && bun run build

FROM golang:1.26.10-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /invoice ./cmd/invoice

FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata && addgroup -g 10001 invoice && adduser -D -u 10001 -G invoice invoice && mkdir -p /data/invoices && chown invoice:invoice /data/invoices
COPY --from=backend /invoice /usr/local/bin/invoice
USER 10001:10001
ENV LISTEN_ADDR=:3001 INVOICE_FILE_ROOT=/data/invoices GIN_MODE=release
EXPOSE 3001
VOLUME ["/data/invoices"]
ENTRYPOINT ["invoice"]
CMD ["serve"]
