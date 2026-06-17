## (1) Scope & Context

The objective is to enhance the `FirmwareUpdateJob` resource to support autonomous discovery of firmware binaries within an OCI registry. Currently, the service requires users to provide an explicit OCI reference (including the SHA digest) for the firmware. We are introducing a declarative "discovery" mode.

When operating in discovery mode, the user will provide a repository path, a hardware model, and a version target (e.g., "latest"). The service's reconciler must autonomously connect to the OCI registry via ORAS, fetch the manifests for all available tags, and filter them using custom OCI annotations. It will check the `dev.fabrica.hardware.compatible` annotation to ensure compatibility with the requested hardware model, and parse the `org.opencontainers.image.version` annotation to find the highest semantic version. Once the highest compatible version is found, the service must extract its SHA digest, proceed with the update, and record the exact resolved version and digest in the resource's status.

Scale and performance are not constraints; the OCI registry will contain a limited number of objects, so brute-force fetching of manifests client-side is the expected approach.

## (2) Code Changes to Implement

1. **Modify Resource Types (`apis/hardware.fabrica.dev/v1/firmwareupdatejob_types.go`):**
* Define a new `DiscoverySpec` struct containing `Repository`, `HardwareModel`, and `Version` (all required strings). Ensure standard `json`, `yaml`, and `validate` tags are applied.
* Update `FirmwareUpdateJobSpec` to include the new `Discovery` struct pointer and change `OCIReference` to a pointer so both are optional, but mutually exclusive.
* Update `FirmwareUpdateJobStatus` to include `ResolvedVersion` and `ResolvedDigest`.
* Update the `Validate(ctx context.Context)` method to enforce that exactly one of `OCIReference` or `Discovery` is provided.


2. **Execute Fabrica Code Generation:**
* Regenerate the supporting boilerplate, API models, and storage schemas by running `fabrica generate && go mod tidy`


3. **Implement Discovery Logic (`pkg/firmwareproxy/resolver.go` & Reconciler):**
* Implement the client-side OCI discovery logic using `oras.land/oras-go/v2`.
* The logic must fetch all tags for the repository provided in the `DiscoverySpec`.
* For each tag, fetch the manifest and inspect its annotations.
* Discard any manifest where the `dev.fabrica.hardware.compatible` annotation does not contain the requested `HardwareModel`.
* Parse the `org.opencontainers.image.version` annotation from the remaining manifests as a Semantic Version.
* Sort the semantic versions to isolate the target version (e.g., the highest version if "latest" is requested).
* Extract the payload digest from the selected manifest.


4. **Update Reconciler (`pkg/reconcilers/firmwareupdatejob_reconciler.go`):**
* Wire the new discovery logic into the reconciliation loop.
* Ensure that upon successful resolution and execution, the `ResolvedVersion` and `ResolvedDigest` fields are written to the `FirmwareUpdateJobStatus`.



## (3) Acceptance Criteria

* The project compiles successfully without errors (`go build ./...`).
* The `Validate` method successfully rejects resource submissions that contain both an `OCIReference` and a `Discovery` block, as well as submissions containing neither.
* The code successfully parses SemVer annotations and correctly identifies the highest version among multiple valid tags.
* The generated Fabrica artifacts (client, OpenAPI, models) accurately reflect the updated Go structs without requiring manual intervention.
* The reconciler correctly updates the `Status` block of the `FirmwareUpdateJob` with the resolved metadata upon completion.

## (4) Output Artifacts

Upon meeting all Acceptance Criteria, generate a `HANDOFF-PHASE2.md` file in the planning directory containing:

1. A brief summary of the implemented logic.
2. The exact, verified `curl` command (or client CLI command) that successfully tested the code.
3. Detailed notes on important details for using the code that was implemented, whereby someone with no context could fully utilize the code as expected and fully understand the implementation.