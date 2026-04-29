# Firmware Management Update Service (FMUS) MVP

## Overview

The Firmware Management Update Service (FMUS) is a declarative API built on the Fabrica framework designed to automate hardware firmware updates across various Baseboard Management Controllers (BMCs). 

Rather than executing imperative scripts, administrators interact with FMUS by declaring a "Desired State" via an `UpdateJob` resource. A background reconciliation controller continuously watches for these jobs, determines the appropriate network transport strategy for the specific hardware, executes the update over the BMC's Redfish API, and writes the outcome back to the resource's "Status" field.

## Architecture

To accommodate the varying Redfish implementations across different hardware vendors, the MVP utilizes a dual-component architecture:

1.  **Fabrica Reconciler (The Orchestrator):** A stateless background worker that evaluates the `UpdateJob`. It handles the HTTP client execution, idempotency, state transitions, and error handling.
2.  **Global Background File Server:** A static HTTP server embedded directly into the Fabrica `main.go` startup sequence. It continuously hosts the local firmware payload directory (e.g., `./firmware_payloads`) to service BMCs that require network file retrieval.

## Firmware Delivery Paradigms

FMUS supports two distinct update strategies to handle different BMC requirements:

* **Push Strategy (Direct Upload):** For BMCs that support `MultipartHttpPushUri`. The reconciler reads the firmware binary from the local disk and streams it directly to the BMC in a `multipart/form-data` POST request. No external server interaction is required.
* **Pull Strategy (Network Retrieval):** For BMCs (such as older Intel hardware) that only support the `SimpleUpdate` action. The reconciler sends a JSON payload to the BMC containing a URL pointing to the Global Background File Server. The BMC then opens its own HTTP client and pulls the file over the network.

---

## User Workflow

The following demonstrates the end-to-end user workflow for executing a firmware update using the `Pull` strategy.

### 1. Stage the Firmware Payload

Before submitting a job, the administrator must ensure the target firmware binary is placed in the local directory monitored by the background file server.

```bash
cp target-firmware.bin ./firmware_payloads/test-firmware.bin
```

### 2. Submit the Desired State (Phase A)

The administrator declares the update intent by creating an `UpdateJob` resource via the Fabrica REST API. The payload includes the target BMC address, credentials, the target filename, and the required update strategy.

```bash
curl -s -X POST http://localhost:8085/updatejobs -H "Content-Type: application/json" -d '{"apiVersion":"example.fabrica.dev/v1","kind":"UpdateJob","metadata":{"name":"pull-update-test"},"spec":{"bmcAddress":"127.0.0.1:8443","username":"root","password":"password123","firmwareFilename":"test-firmware.bin","updateStrategy":"Pull"}}'
```

### 3. Asynchronous Execution (Phase B)

Upon creation, the Fabrica event bus triggers the reconciliation controller. The reconciler transitions the resource to a `Provisioning` state, formats the Redfish payload containing the internal file server URL, and executes the request against the BMC. 

### 4. Verify the Outcome

The administrator can poll the API using the generated resource UID to view the final observed state of the update. The `status` block will display the terminal phase, a detailed message, and the completion timestamp.

```bash
curl -s http://localhost:8085/updatejobs/updatejob-59839841
```

**System Response:**

```json
{
  "apiVersion": "v1",
  "kind": "UpdateJob",
  "metadata": {
    "name": "pull-update-test",
    "uid": "updatejob-59839841",
    "createdAt": "2026-04-29T10:30:14.559491-07:00",
    "updatedAt": "2026-04-29T10:30:14.579886-07:00"
  },
  "spec": {
    "bmcAddress": "127.0.0.1:8443",
    "username": "root",
    "password": "password123",
    "firmwareFilename": "test-firmware.bin",
    "updateStrategy": "Pull"
  },
  "status": {
    "Phase": "Complete",
    "message": "Update accepted by BMC. HTTP Status: 202",
    "completionTime": "2026-04-29T10:30:14.578579-07:00"
  }
}
```