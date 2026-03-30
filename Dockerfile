FROM golang:1.22 AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o tls-cert-exporter ./cmd

FROM gcr.io/distroless/base-debian12

WORKDIR /app
COPY --from=builder /app/tls-cert-exporter /app/tls-cert-exporter

ENV CONFIG_PATH=/app/config.yaml

USER nonroot:nonroot

EXPOSE 2112

ENTRYPOINT ["/app/tls-cert-exporter"]