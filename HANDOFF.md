# Firmware Management Service (FMS) - Implementation Handoff

## Business Logic Summary

The Firmware Management Service is a unified service that acts as a local firmware library and orchestrates out-of-band Redfish firmware updates for hardware nodes. The implementation includes:

### 1. HTTP File Server
- Mounted at `/firmware-files/` endpoint
- Serves firmware binary files from the local `./firmware_payloads` directory
- Automatically creates the directory if it doesn't exist
- Allows BMCs to PULL firmware files via HTTP

### 2. FirmwareImage Reconciler
- **Trigger:** Creation or Update of a FirmwareImage resource
- **Action:** Validates that the firmware binary file exists in `./firmware_payloads`
- **State Update:** Sets `Status.Verified = true` if file exists and is a regular file; otherwise sets `Verified = false` and records error message

### 3. FirmwareUpdateJob Reconciler
- **Trigger:** Creation or Update of a FirmwareUpdateJob resource
- **Action:**
  1. Retrieves the corresponding FirmwareImage resource by name
  2. Verifies that the FirmwareImage has been validated
  3. Constructs the firmware file URI: `http://localhost:8090/firmware-files/<filename>`
  4. Executes HTTP POST to Redfish endpoint: `https://<TargetAddress>/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate`
  5. Sends JSON payload with ImageURI and TransferProtocol
  6. Uses basic authentication with provided credentials (TLS verification disabled)
- **State Update:**
  - On success (200/202): Sets Status to `InProgress`, records Redfish Task ID if available
  - On failure: Sets Status to `Failed`, records exact error message
  - Records StartTime when first executed
  - Records EndTime on failure

## Schema Definitions

### FirmwareImage Resource

**Spec Fields:**
- `filename` (string, required): The exact name of the binary file on disk
- `version` (string, required): Semantic version string (e.g., "1.0.0")
- `targetComponent` (string, required): Target component (e.g., "BIOS", "BMC")
- `models` (array of strings, required, min 1): Supported hardware models (e.g., ["Dell PowerEdge R740"])

**Status Fields:**
- `verified` (boolean): Whether the file was successfully found and verified on disk
- `error` (string, optional): Error message if verification failed (e.g., "File not found: dummy.bin")

### FirmwareUpdateJob Resource

**Spec Fields:**
- `targetAddress` (string, required): Hostname or IP address of the BMC
- `username` (string, required): BMC authentication username
- `password` (string, required): BMC authentication password
- `imageName` (string, required): Name of the FirmwareImage resource to use

**Status Fields:**
- `status` (string, optional): Job status ("Pending", "InProgress", "Success", "Failed")
- `redfishTaskID` (string, optional): Redfish Task ID returned by BMC
- `startTime` (*time.Time, optional): RFC3339 timestamp when update was initiated
- `endTime` (*time.Time, optional): RFC3339 timestamp when update completed or failed
- `error` (string, optional): Detailed error message if job failed

## Verified curl Commands

### 1. File Server Test - GET Firmware File
```bash
curl -v http://localhost:8080/firmware-files/dummy.bin
```
Expected Response: `HTTP/1.1 200 OK` with file content

### 2. Create FirmwareImage Resource
```bash
curl -X POST http://localhost:8080/firmwareimages \
  -H "Content-Type: application/json" \
  -d '{
    "apiVersion": "hardware.fabrica.dev/v1",
    "kind": "FirmwareImage",
    "metadata": {
      "name": "test-firmware-v1",
      "uid": "firmwareimage-001"
    },
    "spec": {
      "filename": "dummy.bin",
      "version": "1.0.0",
      "targetComponent": "BIOS",
      "models": ["Dell PowerEdge R740"]
    }
  }'
```
Expected Response: `HTTP/1.1 201 Created` with resource JSON and `"verified":true` after reconciliation

### 3. Retrieve FirmwareImage Resource (After Reconciliation)
```bash
curl http://localhost:8080/firmwareimages/firmwareimage-793d9296
```
Expected Response: `HTTP/1.1 200 OK` with resource including `"verified":true` in status

### 4. Create FirmwareUpdateJob Resource
```bash
curl -X POST http://localhost:8080/firmwareupdatejobs \
  -H "Content-Type: application/json" \
  -d '{
    "apiVersion": "hardware.fabrica.dev/v1",
    "kind": "FirmwareUpdateJob",
    "metadata": {
      "name": "update-job-001",
      "uid": "firmwareupdatejob-001"
    },
    "spec": {
      "targetAddress": "192.168.1.100",
      "username": "admin",
      "password": "admin123",
      "imageName": "test-firmware-v1"
    }
  }'
```
Expected Response: `HTTP/1.1 201 Created` with empty status initially

### 5. Retrieve FirmwareUpdateJob Resource (After Reconciliation)
```bash
curl http://localhost:8080/firmwareupdatejobs/firmwareupdatejob-ce8f73df
```
Expected Response: `HTTP/1.1 200 OK` with updated status showing `"status":"Failed"` and error message (expected since no real BMC endpoint exists)

## Implementation Details

### Project Structure
- **API Group:** hardware.fabrica.dev
- **Storage Type:** ent (Ent ORM with SQLite)
- **Reconciliation:** Enabled with 5 workers
- **Events:** Enabled with in-memory event bus

### Key Files
- `apis/hardware.fabrica.dev/v1/firmwareimage_types.go` - FirmwareImage resource definition
- `apis/hardware.fabrica.dev/v1/firmwareupdatejob_types.go` - FirmwareUpdateJob resource definition
- `pkg/reconcilers/firmwareimage_reconciler.go` - FirmwareImage validation logic
- `pkg/reconcilers/firmwareupdatejob_reconciler.go` - FirmwareUpdateJob execution logic
- `cmd/server/custom_routes.go` - HTTP file server route registration

### Server Port
- Default: `8080`
- File server available at: `http://localhost:8080/firmware-files/`

### Testing Notes
- The FirmwareImage reconciler successfully verified the test file in `./firmware_payloads/dummy.bin`
- The FirmwareUpdateJob reconciler correctly handled connection failures to non-existent BMC endpoints
- Both reconcilers properly update status fields and record error messages
- All HTTP endpoints return appropriate status codes (201 for creation, 200 for retrieval)

## Next Steps for Deployment

1. Configure actual BMC endpoints in FirmwareUpdateJob resources
2. Implement monitoring for job completion via Redfish Task polling
3. Add webhook support for BMC notifications
4. Implement rate limiting for Redfish API calls
5. Add support for image staging and verification before update
6. Implement rollback capabilities on update failure
