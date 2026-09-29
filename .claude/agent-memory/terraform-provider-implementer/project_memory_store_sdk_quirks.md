---
name: project-memory-store-sdk-quirks
description: SDK v1.67.0 quirks hit implementing anthropic_memory_store (#119/#120) — service field name, actual beta header, and the metadata-PATCH escape hatch for a map[string]string param field.
metadata:
  type: project
---

Implementing `client.Beta.MemoryStore*` (memory stores, beta):

- The SDK service field on `BetaService` is `MemoryStores` (plural), not `MemoryStore` — despite the type names being `BetaMemoryStoreService`, `BetaMemoryStoreNewParams`, etc. (singular). Always grep `beta.go` for the actual field name rather than guessing from the type name pattern used elsewhere in the SDK.
- The SDK sends beta header `agent-memory-2026-07-22` for every memory-store call (hardcoded in `betamemorystore.go`, not user-settable via a simple option). The issue #119/#120 bodies say `managed-agents-2026-04-01` — that's stale/wrong for this specific service; trust the SDK source over the issue text. Not yet verified against the live API (unlike the vaults/WIF headers documented in CLAUDE.md), so treat as SDK-source-confirmed only.
- `BetaMemoryStoreUpdateParams.Metadata` is typed `map[string]string` (not `map[string]any`), yet its doc comment says "set a key to null to delete it" — a plain Go map[string]string cannot represent a per-key null. Same escape hatch as vaults' `buildMetadataPatch`: build the patch as `map[string]any` and send it via `params.SetExtraFields(map[string]any{"metadata": patch})` instead of assigning `params.Metadata` directly. Confirmed this compiles and the acceptance test that removes a key (`TestAccMemoryStoreResource_update`) passes live.
- Live-tested (2026-09-29, via `TestAcc*` in `internal/services/memorystores`): create/read/update/delete/archive/list all work as documented; updating an archived store correctly 4xx's-would-happen but the resource's own guard now catches it before the API call, so this wasn't directly observed against the live API — only the resource-level guard was exercised.
