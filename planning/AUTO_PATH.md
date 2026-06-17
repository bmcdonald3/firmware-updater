You are absolutely right. Forcing a sysadmin to memorize and type out exact DMTF routing paths like `/redfish/v1/UpdateService/FirmwareInventory/Node1.BIOS` completely defeats the purpose of building an automated, high-level orchestration tool.

The engineering instinct to "test multiple options" is close, but we can do one better. Redfish is a self-describing REST API. Instead of brute-forcing multiple paths, the Fabrica service can simply query the root `UpdateService` endpoint. The controller will respond with a JSON document that explicitly tells our service exactly what its specific `SimpleUpdate` URI is, and it can list its entire firmware inventory so we can search it dynamically.

By implementing **Redfish Auto-Discovery**, a sysadmin only needs to provide human-readable intent, like `"component": "BIOS"` or `"component": "BMC"`. The reconciler will do the heavy lifting of translating that into the exact URIs required by that specific chassis or cabinet controller.

Here is the revised implementation plan to hand off to your agent, starting from your JIT hardcoded JIT JIT branch.

---

# Phase 2: Reconciliation Implementation - Auto-Discovering Redfish Dispatcher

## 1. Context Acquisition

Read `apis/hardware.fabrica.dev/v1/firmwareupdatejob_types.go`. The current schema requires users to provide explicit Redfish `Targets`. We are shifting to an intent-based, auto-discovery model. Do not modify the underlying database driver or storage types.

## 2. Schema Modifications

Update the `FirmwareUpdateJobSpec` struct to replace explicit routing with human-readable intent.

* Make the existing `Targets []string` field optional (`omitempty`).
* Add a new field: `Component string` (e.g., "BMC", "BIOS", "CabinetController"). Add `omitempty` JSON tags.

## 3. Reconciliation State Machine

Implement the following discovery logic in `pkg/reconcilers/firmwareupdatejob_reconciler.go` *after* the JIT OCI resolution but *before* the POST dispatch.

* **Execution Steps:**
* **Step 1: Action URI Discovery.** Execute an HTTP GET to `https://[TargetAddress]/redfish/v1/UpdateService` (using the provided credentials and `InsecureSkipVerify`).
Parse the JSON response. Locate the `Actions` object. Extract the `target` string from either `#UpdateService.SimpleUpdate` or `#SimpleUpdate` (vendors use both). Store this as the resolved Dispatch URI.
* **Step 2: Target Component Discovery.**
If the user provided `Spec.Component` (e.g., "BIOS") instead of explicit `Targets`, execute an HTTP GET to `https://[TargetAddress]/redfish/v1/UpdateService/FirmwareInventory`.
Iterate through the `Members` array. For each member, GET its `@odata.id` URI.
Inspect the `Id`, `Name`, and `Description` fields of that member. If any of those fields contain a case-insensitive match for the `Spec.Component` string, append that `@odata.id` to your resolved `Targets` slice.
* **Step 3: Dynamic Dispatch.**
Construct the `SimpleUpdate` JSON payload using the resolved `Targets` array and `ImageURI`. POST it to the resolved Action URI discovered in Step 1. Ensure `"TransferProtocol": "HTTP"` is included in the payload.


* **Error Handling:**
* **Transient Errors:** Network timeouts or 5xx errors while querying the discovery endpoints should trigger the standard exponential backoff retry.
* **Terminal Errors:** * The `UpdateService` JSON does not contain a `SimpleUpdate` action.
* The auto-discovery loop finishes checking all inventory members and finds 0 matches for the requested `Spec.Component`.





## 4. State Updates

Update the resource's `Status` field based on discovery and dispatch.

* **On Success:** Set `Status.JobState = "InProgress"` and log the discovered Action URI in the debug logs.
* **On Transient Failure:** Leave state in `Resolving` and append the network timeout error.
* **On Terminal Failure (Discovery Failed):** Set `Status.JobState = "Failed"` and append explicit error messages (e.g., "auto-discovery failed: component 'BIOS' not found in FirmwareInventory").

## 5. Acceptance Criteria

* **Code Generation:** Run `fabrica generate` to rebuild the models, openapi specs, and storage layers with the new `Component` field.
* **Compilation:** Run `go mod tidy` and `go build ./...`.
* **Idempotency Verification:** The reconciler must remain idempotent. If the state is `InProgress`, it must not re-run the discovery GET requests or the dispatch POST request.
* **Testing:** Stage a dummy payload in the local OCI registry. Verify the auto-discovery by executing a job creation POST using only the human-readable `component` field:

```bash
curl -sS -X POST http://127.0.0.1:8090/firmwareupdatejobs/ -H 'Content-Type: application/json' -d '{"metadata":{"name":"auto-discover-test"},"spec":{"targetAddress":"10.104.0.40","username":"root","password":"initial0","ociReference":"127.0.0.1:5000/firmware/bios:1.8.2","serverProxyAddress":"10.254.1.20","component":"BIOS"}}'

```

## 6. Output Artifacts

Generate a `planning/AUTO-path_HANDOFF.md` containing:

1. A brief summary of the implemented reconciliation logic.
3. The exact, verified `curl` command that shows successful execution of the code.
4. Detailed notes on important details for using the code, whereby someone with no context could fully utilize the code you wrote and endpoints as expected and fully understand the implementation.