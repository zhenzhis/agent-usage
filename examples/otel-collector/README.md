# OpenTelemetry Collector Example

This example forwards local OTLP HTTP traces to Agent Ledger's disabled-by-default OTLP HTTP receiver.

Agent Ledger must be configured with:

```yaml
integrations:
  otlp_receiver:
    enabled: true
    max_body_bytes: 4194304
    max_spans: 1000
```

The collector exporter uses `endpoint: http://127.0.0.1:9800`, which sends trace batches to `POST /v1/traces`. Keep this endpoint loopback-only unless you have explicit local auth and network controls.

Agent Ledger returns per-request backpressure headers on accepted and rejected OTLP batches:

- `X-Agent-Ledger-OTLP-Backpressure`
- `X-Agent-Ledger-OTLP-Body-Bytes`
- `X-Agent-Ledger-OTLP-Max-Body-Bytes`
- `X-Agent-Ledger-OTLP-Spans`
- `X-Agent-Ledger-OTLP-Max-Spans`
- `X-Agent-Ledger-OTLP-Events`

Over-limit body or span batches are rejected explicitly and recorded as metadata-only audit events. Increase `max_body_bytes`, lower collector `send_batch_size`, or reduce exporter queue pressure when these headers report `body_limit_exceeded` or `span_limit_exceeded`.

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
