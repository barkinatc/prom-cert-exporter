# TLS Certificate Expiry Exporter

## Overview

This project is a Prometheus exporter that monitors TLS certificate expiration dates for remote URLs.  
It checks configured endpoints, calculates remaining time until expiry, and exposes metrics for Prometheus.

It is packaged with Docker and can be deployed to Kubernetes using the included Helm chart.

---

## Metrics

The exporter exposes the following metrics:

- `tls_cert_expiry_timestamp_seconds` – Certificate expiry time (Unix timestamp)
- `tls_cert_remaining_days` – Remaining days until expiry
- `tls_cert_probe_success` – Whether the TLS probe succeeded (1/0)
- `tls_cert_probe_duration_seconds` – Probe duration in seconds

---

## Configuration

Example config:

```yaml
targets:
  - https://www.google.com
  - https://www.github.com

scrape_interval: 30s
listen_address: ":2112"
```

## Run locally

```bash
go mod tidy
go run ./cmd
```

Then access:

```text
http://localhost:2112/metrics
```

## Docker

Build image:

```bash
docker build -t barkinatici/ssl-expire:1.0.0 .
```

Run container:

```bash
docker run -p 2112:2112 barkinatici/ssl-expire:1.0.0
```

## Helm (Kubernetes)

Helm chart is available under:

```text
charts/ssl-expire
```

Deploy:

```bash
helm upgrade --install ssl-expire ./charts/ssl-expire -n monitoring --create-namespace
```

## Alerting

This exporter is designed to be used with Prometheus alert rules.

Example alerts:

* Certificate expiring soon
* Certificate expired
* Endpoint unreachable

## Project Structure

```text
cmd/                    # application entrypoint
Dockerfile              # container build definition
config.yaml             # example configuration
charts/ssl-expire/      # Helm chart for Kubernetes deployment
```
