### Part 1: Declarative Workflow Mapping

This workflow adheres to the Kubernetes-style reconciliation loop, separating user intent from system execution.

**Phase A: Desired State (User Submission)**
1.  **File Placement:** The administrator ensures the target firmware binary is located in the local directory served by the global background HTTP server (e.g., `/var/www/firmware/`).
2.  **Resource Creation:** The administrator submits an HTTP POST request to the Fabrica API to create an `UpdateJob` resource.
3.  **Data Ingestion:** The JSON payload contains the specific `Spec` inputs: BMC network address, credentials, the target firmware filename, and the required update strategy (Push or Pull).
4.  **Initial State:** The Fabrica API validates the input, saves the resource to the database with an initial default `Status` phase of "Pending", and returns an HTTP 201 Created response to the user.

**Phase B: Background Reconciliation (Asynchronous Execution)**
1.  **Event Trigger:** The creation of the `UpdateJob` resource publishes an `io.fabrica.updatejob.created` CloudEvent to the internal memory bus.
2.  **Worker Initialization:** The background reconciliation controller dequeues the event and loads the `UpdateJob` resource from the database.
3.  **State Evaluation:** The reconciler checks the current `Status.Phase`. If it is "Complete" or "Error", the reconciler exits (idempotency). If it is "Pending", it transitions the phase to "Provisioning" and saves the state.
4.  **Strategy Execution:**
    * **If Strategy == Pull:** The reconciler formats an `ImageURI` string pointing to the global background file server and sends an HTTP POST payload to the BMC's `SimpleUpdate` endpoint.
    * **If Strategy == Push:** The reconciler reads the file from the local directory and streams it as `multipart/form-data` to the BMC's `MultipartHttpPushUri` endpoint.
5.  **Output Tracking:** The reconciler parses the HTTP response code from the BMC.
    * **Success:** If the BMC returns a 200 or 202, the reconciler updates the `Status.Phase` to "Complete" and notes the success in the `Status.Message`.
    * **Failure:** If the BMC returns a 400 or 500-level error, the reconciler updates the `Status.Phase` to "Error" and writes the raw BMC error response into the `Status.Message` for debugging.
6.  **Finalization:** The updated `Status` is saved to the Fabrica database. The user can poll the resource URI at their convenience to view the outcome.

---

### Part 2: Required API Resources and Schema Definitions

To facilitate this workflow, you need a single primary resource: `UpdateJob`. 

Below are the Go struct definitions for the `UpdateJobSpec` (Desired State) and `UpdateJobStatus` (Observed State).

```go
package v1

import "time"

// UpdateJobSpec defines the desired state of the firmware update provided by the user.
type UpdateJobSpec struct {
	BMCAddress       string `json:"bmcAddress" validate:"required"`
	Username         string `json:"username" validate:"required"`
	Password         string `json:"password" validate:"required"`
	FirmwareFilename string `json:"firmwareFilename" validate:"required"`
	UpdateStrategy   string `json:"updateStrategy" validate:"oneof=Push Pull"`
}

// UpdateJobStatus defines the observed state of the firmware update managed by the reconciler.
type UpdateJobStatus struct {
	Phase          string     `json:"phase" validate:"oneof=Pending Provisioning Complete Error"`
	Message        string     `json:"message,omitempty"`
	CompletionTime *time.Time `json:"completionTime,omitempty"`
}
```