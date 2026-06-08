package integrations

import "github.com/zhenzhis/agent-ledger/internal/storage"

// EnrichRuntimeStatus attaches stable contract identity and compatibility
// hashes to a process-local runtime status payload.
func EnrichRuntimeStatus(status *storage.RuntimeStatus, opts Options) *storage.RuntimeStatus {
	if status == nil {
		return nil
	}
	status.Contract = "agent-ledger.runtime-status"
	status.Version = "v1"
	status.CapabilityCatalogHash = CatalogFingerprint(opts)
	status.CanonicalSchemaHash = storage.CanonicalEventSchemaFingerprint()
	status.AdapterSpecHash = AdapterContractFingerprint()
	return status
}
