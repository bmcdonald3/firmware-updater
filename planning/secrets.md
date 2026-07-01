### (1) Project Scope and Context

The objective is to replace the insecure practice of hardcoding plain-text usernames and passwords for `curl` executions with a secure, encrypted storage mechanism. This will be achieved by integrating the `[github.com/OpenCHAMI/magellan/pkg/secrets](https://github.com/OpenCHAMI/magellan/pkg/secrets)` library, specifically utilizing its `LocalSecretStore` implementation.

The integration requires configuring the deployment environment to supply a 64-character hex-encoded AES-256 GCM master key (`MASTER_KEY`). The application will instantiate the secret store once, maintaining it in the global state. Credentials will be serialized into JSON format, encrypted, and written to a local file. At execution time, the application will retrieve and decrypt the credentials, deserialize them, and inject them dynamically into the `curl` command.

Because this project is a Fabrica-based service, modifying the data model to remove plain-text credential fields requires updating the resource specifications and subsequently running `fabrica generate` to ensure the generated boilerplate (e.g., models and routes) remains functional and synchronized with the updated schema.

### (2) Implementation Code Changes

To implement this specification, the following discrete changes must be made to the codebase:

* **Resource Model Updates:**
* Locate the Fabrica resource definition files (e.g., `apis.yaml` or relevant `.yaml` files in `apis/`).


* Remove the `username` and `password` properties from the definitions.


* Run `fabrica generate` at the project root to update `cmd/server/models_generated.go`, `cmd/server/routes_generated.go`, and any dependent internal storage packages.




* **Environment Configuration & State Initialization:**
* In the server initialization block (e.g., `cmd/server/main.go`), import `[github.com/OpenCHAMI/magellan/pkg/secrets](https://github.com/OpenCHAMI/magellan/pkg/secrets)`.


* Read the `MASTER_KEY` environment variable. If it is empty or invalid, the application must terminate with a fatal error.
* Call `secrets.OpenStore("secrets.json")` (or another designated filename) and attach the returned `SecretStore` interface to the central application state/context. Ensure this is instantiated exactly once to avoid I/O conflicts.


* **Credential Storage Logic:**
* Where credentials are initially provided to the system, create a serialized JSON string: `{"username":"<user>","password":"<pass>"}`.
* Call `StoreSecretByID(secretID, credentials)` using the application state's `SecretStore` instance to encrypt and persist the payload.


* **Credential Retrieval and Execution Logic:**
* Locate the function responsible for building and executing the `curl` command.
* Call `GetSecretByID(secretID)` to retrieve the decrypted JSON string.
* Parse the JSON string back into discrete `username` and `password` variables.
* Inject these variables into the `curl` subprocess arguments (e.g., `-u`, `username:password`).



### (3) Acceptance Criteria

* **AC1:** The application fails to start if the `MASTER_KEY` environment variable is not set to a valid 64-character hex-encoded string.
* **AC2:** Plain-text fields for `username` and `password` are removed from the Fabrica resource models, and `fabrica generate` completes without syntax or generation errors.


* **AC3:** The `SecretStore` interface is instantiated only once and is accessible via the application state.
* **AC4:** Supplying a username and password results in a serialized JSON string being encrypted and written to the target local JSON file via `StoreSecretByID`.
* **AC5:** Execution of the `curl` workflow successfully retrieves and decrypts the credentials via `GetSecretByID` without reading plain-text from the file system.
* **AC6:** The application parses the decrypted JSON payload and dynamically populates the `curl` command.
* **AC7:** The implemented `curl` execution is manually tested and verified to succeed against the target endpoint using the dynamically retrieved credentials.

### (4) Handoff Document Generation

```markdown
## Output Artifacts

Upon meeting all Acceptance Criteria, generate a `HANDOFF-PHASE2.md` file in the planning directory containing:

1. A brief summary of the implemented logic, including the file path of the chosen encrypted JSON store and how the application state manages the `SecretStore` interface.
2. The exact, verified `curl` command that successfully tested the code.
3. Detailed notes on important details for using the code that was implemented, whereby someone with no context could fully utilize the code as expected and fully understand the implementation. This must include instructions on generating the `MASTER_KEY` and setting it in the environment.

```