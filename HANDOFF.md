# Firmware Management Service (FMS) - Implementation Handoff

## Summary

The Firmware Management Service has been successfully implemented as a Fabrica-based microservice that:

1. **Manages Firmware Images** - Tracks firmware binaries in a local directory accessible via HTTP
2. **Orchestrates Redfish Firmware Updates** - Sends firmware update commands to hardware BMCs via Redfish API
3. **Implements Reconciliation** - Automatically validates firmware files and processes firmware update jobs
4. **Provides HTTP File Server** - Serves firmware files from the `/firmware-files` endpoint

The service uses SQLite for persistent storage and supports CloudEvents for lifecycle events.

## Schema Definition

### FirmwareImage Resource

**Spec Fields:**
- `filename` (string, required): The exact name of the binary file on disk in the `firmware_payloads` directory
- `version` (string, required): Semantic version string (e.g., "1.0.0")
- `targetComponent` (string, required): Target hardware component (e.g., "BIOS", "BMC")
- `models` (array of strings, required): Array of supported hardware model identifiers

**Status Fields:**
- `verified` (boolean): True if the firmware file was found and verified on disk
- `error` (string, optional): Error message if file verification failed

### FirmwareUpdateJob Resource

**Spec Fields:**
- `targetAddress` (string, required): Hostname or IP of the BMC
- `username` (string, required): BMC credentials username
- `password` (string, required): BMC credentials password
- `imageName` (string, required): Reference to the FirmwareImage resource name (not UID)
- `targets` (array of strings, required): Array of Redfish OData URIs to update (e.g., `["/redfish/v1/UpdateService/FirmwareInventory/BMC"]`)
- `serverAddress` (string, required): IP or hostname of this Fabrica server (used in firmware download URI, NOT localhost)

**Status Fields:**
- `status` (string): Job status ("Pending", "InProgress", "Completed", "Failed")
- `taskId` (string, optional): Redfish Task URI if returned by BMC
- `startTime` (string, optional): RFC3339 timestamp when update was initiated
- `endTime` (string, optional): RFC3339 timestamp when update completed or failed
- `error` (string, optional): Exact error message if job failed

## Business Logic Implementation

### 1. HTTP File Server (Requirement 1)
- **Location**: Implemented in `cmd/server/openapi_extensions.go`
- **Endpoint**: `/firmware-files/`
- **Function**: Serves files from `./firmware_payloads` directory
- **Behavior**: Directory is auto-created if it doesn't exist

### 2. FirmwareImage Validation (Requirement 2)
- **Location**: Implemented in `pkg/reconcilers/firmwareimage_reconciler.go`
- **Trigger**: Resource creation/update events
- **Logic**: Checks if `Spec.Filename` exists in `./firmware_payloads` directory
- **Action**: Sets `Status.Verified` to true/false and populates `Status.Error` if file not found

### 3. FirmwareUpdateJob Execution (Requirement 3)
- **Location**: Implemented in `pkg/reconcilers/firmwareupdatejob_reconciler.go`
- **Trigger**: Resource creation/update events
- **Steps**:
  1. Skips if status is already "InProgress", "Completed", or "Failed" (idempotency)
  2. Retrieves FirmwareImage by name using storage List and filter
  3. Constructs Image URI: `http://[ServerAddress]:8090/firmware-files/[Filename]`
  4. Executes HTTP POST to: `https://[TargetAddress]/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate`
  5. JSON payload: `{"ImageURI": "<URI>", "Targets": ["<Target1>", ...]}`
  6. Uses basic auth with provided credentials
  7. Disables TLS verification (InsecureSkipVerify: true)
  8. Updates Status based on response:
     - 200/202 → Status="InProgress", extracts TaskID if available
     - Other status codes → Status="Failed", stores exact error message

## Verified Endpoints and Commands

### 1. Health Check
```bash
curl http://localhost:8080/health
```
**Response**: `{"status":"healthy","service":"firmware-manager"}`

### 2. File Server - Download Firmware
```bash
curl http://localhost:8080/firmware-files/dummy.bin
```
**Response**: File content (binary)

### 3. Create FirmwareImage Resource
```bash
curl -X POST http://localhost:8080/firmwareimages \
  -H "Content-Type: application/json" \
  -d '{
    "apiVersion": "hardware.fabrica.dev/v1",
    "kind": "FirmwareImage",
    "metadata": {"name": "bios-1.0.0"},
    "spec": {
      "filename": "dummy.bin",
      "version": "1.0.0",
      "targetComponent": "BIOS",
      "models": ["Dell-R750"]
    }
  }'
```
**Response**: 201 Created with resource including `status.verified: true` (after reconciliation)

### 4. List FirmwareImage Resources
```bash
curl http://localhost:8080/firmwareimages
```
**Response**: JSON array of all FirmwareImage resources

### 5. Create FirmwareUpdateJob Resource
```bash
curl -X POST http://localhost:8080/firmwareupdatejobs \
  -H "Content-Type: application/json" \
  -d '{
    "apiVersion": "hardware.fabrica.dev/v1",
    "kind": "FirmwareUpdateJob",
    "metadata": {"name": "update-bios-001"},
    "spec": {
      "targetAddress": "192.168.1.100",
      "username": "admin",
      "password": "password",
      "imageName": "bios-1.0.0",
      "targets": ["/redfish/v1/UpdateService/FirmwareInventory/BIOS"],
      "serverAddress": "192.168.1.1"
    }
  }'
```
**Response**: 201 Created with resource, Status will be updated to "Failed" (unreachable endpoint in test) or "InProgress" (if Redfish endpoint is available)

### 6. List FirmwareUpdateJob Resources
```bash
curl http://localhost:8080/firmwareupdatejobs
```
**Response**: JSON array of all FirmwareUpdateJob resources with current status

## Testing

Unit tests are located in `pkg/reconcilers/reconcilers_test.go` and cover:
- File existence validation for FirmwareImage
- File not found scenarios
- Idempotency for update jobs (skipping already processed jobs)
- Resource spec/status field structures

Run tests with: `go test ./...`

## Starting the Service

```bash
cd firmware-manager
go run ./cmd/server serve --database-url="file:data.db?cache=shared&_fk=1"
```

The server listens on `0.0.0.0:8080` by default.

## Project Structure

```
firmware-manager/
├── apis/hardware.fabrica.dev/v1/
│   ├── firmwareimage_types.go          # FirmwareImage resource definition
│   └── firmwareupdatejob_types.go      # FirmwareUpdateJob resource definition
├── cmd/server/
│   ├── main.go                         # Server entry point
│   ├── openapi_extensions.go           # Custom HTTP file server route
│   └── *_handlers_generated.go         # Generated API handlers
├── pkg/reconcilers/
│   ├── firmwareimage_reconciler.go     # FirmwareImage validation logic
│   ├── firmwareupdatejob_reconciler.go # Redfish update execution logic
│   └── reconcilers_test.go             # Unit tests
├── internal/storage/
│   └── ent_*                           # Generated Ent ORM code
└── firmware_payloads/                  # Firmware binary directory (auto-created)
```

## Notes

- The service implements strict idempotency: update jobs are only processed once
- File verification for FirmwareImage occurs automatically via reconciliation
- Redfish requests use insecure TLS verification for development/testing
- The serverAddress in FirmwareUpdateJob Spec should NOT be "localhost" but the actual accessible IP/hostname
- Port 8090 is used for firmware file downloads (constructed in the imageURI)
