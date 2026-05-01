# Service Specification: Firmware Management Service (FMS)

## 1. System Overview
**Objective:** A unified service that acts as a local firmware library and orchestrates out-of-band Redfish firmware updates for hardware nodes.
**Primary Domain:** Hardware Lifecycle / Firmware Management.
**Boundaries:** This service tracks available firmware files, serves those files over HTTP, and triggers Redfish update commands. It does NOT automatically reboot nodes, track complex cross-component dependencies, or perform in-band OS-level script updates.

## 2. Infrastructure & Scaffold Configuration
This service relies on the Fabrica framework.
* **Project Name:** firmware-manager
* **API Group:** hardware.fabrica.dev
* **Storage Type:** ent
* **Database Driver:** sqlite
* **Required Features:** --reconcile, --events, --storage

## 3. Resource Requirements (Agent-Designed Schema)

### Resource: FirmwareImage
* **Description:** Represents a firmware binary that exists in a local directory accessible to this service.
* **Data to Capture (Spec):** * `Filename` (The exact name of the binary file on disk).
  * `Version` (Semantic version string).
  * `TargetComponent` (e.g., BIOS, BMC).
  * `Models` (Array of strings representing supported hardware models).
* **State to Track (Status):** A boolean indicating if the file was successfully found and verified on the local disk, and a string for any errors.

### Resource: FirmwareUpdateJob
* **Description:** Represents a request to update a specific node using a specific FirmwareImage.
* **Data to Capture (Spec):**
  * `TargetAddress` (Hostname or IP of the BMC).
  * `Username` and `Password` (Credentials for the BMC).
  * `ImageName` (Reference to the `metadata.name` of a `FirmwareImage` resource).
  * `Targets` (Array of strings representing the Redfish OData URIs to update, e.g., ["/redfish/v1/UpdateService/FirmwareInventory/BMC"]).
  * `ServerAddress` (The actual network IP or hostname of this Fabrica server, so the BMC knows where to download the file from. DO NOT use localhost).
* **State to Track (Status):** Job status string, the Redfish Task ID (if returned), start/end timestamps, and exact failure reasons.

## 4. Custom Business Logic & Reconciliation

**Requirement 1: HTTP File Server (The "Library")**
* In `cmd/server/openapi_extensions.go` or a custom routing file, register a standard Go `http.FileServer` mounted at `/firmware-files/` that serves files from a local `./firmware_payloads` directory. Ensure the directory is created if it does not exist.

**Requirement 2: FirmwareImage Validation**
* **Trigger:** Creation or Update of a `FirmwareImage`.
* **Action:** Check if `Filename` exists in the `./firmware_payloads` directory.
* **State Update:** Set the Status to verified/unverified based on file existence.

**Requirement 3: FirmwareUpdateJob Execution**
* **Trigger:** Creation or Update of a `FirmwareUpdateJob`.
* **Action:** 1. Retrieve the corresponding `FirmwareImage` resource using the provided `ImageName` to get the `Filename`.
  2. Construct the Image URI using the `ServerAddress` provided in the Spec. The URI MUST look like: `http://[ServerAddress]:8090/firmware-files/[Filename]`.
  3. Execute an HTTP POST to the target BMC: `https://[TargetAddress]/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate`.
  4. The JSON payload must ONLY include the `ImageURI` and the `Targets` array provided in the Spec. Example: `{"ImageURI": "<Constructed_URI>", "Targets": ["<Target1>"]}`. Do not include TransferProtocol.
  5. Use basic auth with the provided credentials and disable TLS verification (`InsecureSkipVerify: true`).
* **State Update:** * If Redfish accepts the job (200 OK or 202 Accepted), update Status to `InProgress` and record the Redfish Task URI if provided.
  * If it fails, record the exact error and set Status to `Failed`.

## 5. Agent Operational Directives (Strict Rules of Engagement)
You are an autonomous software engineering agent. You must achieve the target state defined in Sections 1-4 by executing terminal commands, writing code, and resolving your own errors.

**Workflow Loop & Savepoints:**
1. **Analyze & Design:** Read the business logic required in Section 4. Determine the exact Go struct fields required for the Spec and Status of the resources listed in Section 3.
2. **Scaffold:** Execute the `fabrica init` command with the parameters defined in Section 2. 
    * *Git Action:* `git add . && git commit -m "chore: scaffold project"`
3. **Define & Generate:** Use `fabrica add resource` for each item in Section 3. Modify the generated `*_types.go` files to implement the schema you designed. Run `fabrica generate`.
    * *Git Action:* `git add . && git commit -m "feat: define resources and generate artifacts"`
4. **Implement:** Write the custom logic defined in Section 4 in the appropriate Fabrica reconciler stubs and routing files.
5. **Verify (Compiler):** You must run `go mod tidy` and `go build ./...` after modifying any Go files. If the compiler outputs errors, you must read the error, modify the code, and re-compile autonomously.
6. **Test (Unit):** Write table-driven tests for the custom reconciliation logic. Run `go test ./...`. Ensure tests pass.
    * *Git Action:* `git add . && git commit -m "feat: implement and test reconciliation logic"`
7. **Verify (Integration):** You must verify the server successfully binds to the port and routes HTTP requests.
    * Create a dummy file in `./firmware_payloads`.
    * Start the server locally in the background using `go run ./cmd/server serve --database-url="file:data.db?cache=shared&_fk=1"`.
    * Execute a `curl` GET request to verify the file server works (e.g., `curl http://localhost:8090/firmware-files/dummy.bin`).
    * Execute a `curl` POST request to create a `FirmwareImage` resource.
    * If the response is a 404, 400, or 500, analyze the server logs, correct the payload or endpoint path, and re-test until you receive a successful 2xx HTTP status code.
    * Terminate the background server process.
8. **Handoff (CRITICAL):** Create a `HANDOFF.md` file in the root directory. This file must contain:
    * A brief summary of the business logic implemented.
    * The exact schema fields decided upon for the Spec and Status.
    * The exact, verified `curl` commands that succeeded in Step 7 for both file retrieval and resource creation.