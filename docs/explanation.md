Here is the exact division of responsibilities between the publisher, the end-user, and the service.

### 1. The Publisher Workflow (What the system requires to function)

The service must be able to trust the metadata in the OCI registry. This means whoever or whatever compiles and uploads the firmware binaries to the registry must adhere to a strict tagging and annotation standard.

* The publisher compiles the binary.
* The publisher uses ORAS to push the binary to the OCI registry.
* During the push, the publisher attaches standard annotations to the OCI manifest. For example:
* `org.opencontainers.image.version=1.2.0`
* `dev.fabrica.hardware.compatible=Model-X,Model-Y`


* The publisher tags the upload with the version number (e.g., `1.2.0`).

### 2. The End-User Workflow (What the user types and needs to know)

The end-user no longer needs to know the SHA digest of the binary, nor do they need to query the OCI registry manually to find out which version is the newest one supported by their specific hardware.

The user only needs to know two things: the IP address of the target they are updating, and the hardware model of that target.

The user applies the following YAML via the Kubernetes API or a Fabrica CLI command:

```yaml
apiVersion: hardware.fabrica.dev/v1
kind: FirmwareUpdateJob
metadata:
  name: update-switch-01
spec:
  targetAddress: "192.168.1.100"
  username: "admin"
  password: "password"
  serverProxyAddress: "proxy.local"
  discovery:
    repository: "registry.local/firmware"
    hardwareModel: "Model-X"
    version: "latest"

```

### 3. The Service Workflow (What the service figures out)

Once the user submits that configuration, the service's reconciliation loop takes over and performs the following operations:

1. It reads the `FirmwareUpdateJob` and sees the `discovery` block.
2. It connects to `registry.local/firmware` using the `oras-go` library.
3. It requests a list of all tags in that repository.
4. It downloads the manifest for each tag.
5. It inspects the annotations on each manifest. If `dev.fabrica.hardware.compatible` does not contain `Model-X`, it discards that manifest.
6. Out of the remaining manifests, it reads the `org.opencontainers.image.version` annotation, parses them as Semantic Versions, and sorts them to find the highest value.
7. It extracts the SHA digest of that specific manifest.
8. It proceeds with the firmware update process using that SHA.
9. It writes the exact version and SHA it selected into the `status` block of the `FirmwareUpdateJob`, providing the user with a permanent record of what "latest" resolved to at that exact moment in time.
