# OpenTelemetry Collector Example

This example forwards local OTLP HTTP traces to Agent Ledger's disabled-by-default OTLP HTTP receiver.

Files:

- `config.yaml` is for a Collector process running on the same host as Agent Ledger. It exports to `http://127.0.0.1:9800`.
- `config.compose.yaml` is for the Docker Compose smoke stack. Collector and smoke containers share Agent Ledger's network namespace and export to `http://127.0.0.1:9800`, preserving the localhost/auth safety model without embedding a token.
- `agent-ledger.smoke.yaml` is an isolated Agent Ledger config with all file collectors disabled and only the OTLP HTTP receiver enabled.
- `docker-compose.smoke.yml` builds Agent Ledger, starts an OpenTelemetry Collector, posts the strict `otlp-resource-spans.json` fixture through the Collector, and fails unless Agent Ledger exposes the fixture in `/api/model-calls`.

Agent Ledger must be configured with:

```yaml
integrations:
  otlp_receiver:
    enabled: true
    max_body_bytes: 4194304
    max_spans: 1000
```

The host-local collector exporter uses `endpoint: http://127.0.0.1:9800`, which sends trace batches to `POST /v1/traces`. Keep this endpoint loopback-only unless you have explicit local auth and network controls.

The Docker smoke stack can be run from the repository root:

```bash
docker compose -f examples/otel-collector/docker-compose.smoke.yml up --build --abort-on-container-exit --exit-code-from smoke
docker compose -f examples/otel-collector/docker-compose.smoke.yml down -v
```

The compose file defaults to `otel/opentelemetry-collector-contrib:0.154.0` and `alpine:3.21`. `OTELCOL_IMAGE` and `SMOKE_IMAGE` may be overridden by CI or an internal registry. The compose file intentionally does not mount local agent session directories or define auth tokens. Its Collector exporter queue and retry are disabled so the smoke run fails fast when Agent Ledger cannot receive the batch.

Agent Ledger accepts uncompressed and `Content-Encoding: gzip` OTLP HTTP JSON/protobuf requests. For gzip requests, the compressed request body and the decoded body are both bounded by `max_body_bytes`. Agent Ledger returns per-request backpressure headers on accepted and rejected OTLP batches:

- `X-Agent-Ledger-OTLP-Backpressure`
- `X-Agent-Ledger-OTLP-Body-Bytes`
- `X-Agent-Ledger-OTLP-Max-Body-Bytes`
- `X-Agent-Ledger-OTLP-Spans`
- `X-Agent-Ledger-OTLP-Max-Spans`
- `X-Agent-Ledger-OTLP-Events`

Over-limit body, decoded gzip body, or span batches are rejected explicitly and recorded as metadata-only audit events. Increase `max_body_bytes`, lower collector `send_batch_size`, or reduce exporter queue pressure when these headers report `body_limit_exceeded`, `decoded_body_limit_exceeded`, or `span_limit_exceeded`.

For auth-enabled Agent Ledger instances, set:

```bash
export AGENT_LEDGER_AUTH_HEADER="Bearer <local-operator-token>"
```

For localhost deployments without auth, remove the `headers.Authorization` line from `config.yaml`.

The `attributes/privacy` processor deletes common prompt, completion, and message attributes before export. Agent Ledger also ignores raw prompt/message attributes during conversion, but deleting them at the collector boundary keeps the local pipeline easier to audit.

Validate the matching fixture without writing SQLite:

```bash
agent-ledger adapter conformance --kind otel --strict --file examples/adapter-fixtures/otlp-resource-spans.json
```
