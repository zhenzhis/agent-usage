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
