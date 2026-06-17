# JIT Firmware Execution Service

## 1. Architectural Overview

The JIT (Just-In-Time) Firmware Execution Service is a stateless orchestration engine designed to deploy firmware binaries directly from OCI registries to hardware controllers (BMCs, Chassis Controllers, Cabinet Controllers) using the Redfish standard.

Unlike traditional firmware management tools, this service maintains zero local inventory. It relies on a declarative, on-demand execution model driven by the `FirmwareUpdateJob` resource.

### The Execution Pipeline

1. **Request:** An HTTP POST is submitted with hardware credentials, the OCI image location, and the target component identifier.
2. **Resolution:** The service connects to the OCI registry via ORAS, extracts the payload manifest, and retrieves the SHA-256 digest of the 58MB firmware binary.
3. **Auto-Discovery:** The service queries the hardware's `UpdateService` to dynamically discover its specific `SimpleUpdate` action URI and parses the `FirmwareInventory` to map the human-readable component request (e.g., "BMC") to an explicit hardware URI.
4. **Dispatch:** The service sends a Redfish POST to the hardware, instructing it to download the firmware from the Fabrica service's internal proxy route (`http://<serverProxyAddress>:8090/firmware-proxy/layer/<digest>`).
5. **Streaming:** The hardware executes an HTTP GET against the proxy endpoint. The proxy initiates a data stream from the upstream OCI registry and utilizes `io.Copy` to flush the bytes directly into the hardware's response buffer without writing to local disk.

## 2. API Specification

### `FirmwareUpdateJob` Spec

* **`targetAddress`** (string): IP address or domain name of the destination hardware interface.
* **`username`** (string): Administrative username for Redfish authentication.
* **`password`** (string): Administrative password for Redfish authentication.
* **`ociReference`** (string): Complete OCI path and tag/digest (e.g., `127.0.0.1:5000/firmware/cray-bmc:1.10.2`).
* **`component`** (string, optional): Human-readable intent string (e.g., "BMC", "BIOS") used to auto-discover hardware targets dynamically.
* **`targets`** (array of strings, optional): Explicit collection of target Redfish URIs. *Note: Either targets or component must be provided.*
* **`serverProxyAddress`** (string): The IPv4 address of this Fabrica service instance from the perspective of the hardware management network.

## 3. Runbook: Cray EX Deployment

This workflow documents the deployment of a 58MB payload to a Cray Non-Compute Node (NCN) over the Hardware Management Network using Redfish Auto-Discovery.

### Step 1: Toolchain Initialization

Required toolchains (Go and ORAS) are installed directly into the localized user directory to bypass HPC environment package managers.

```bash
mkdir -p /scratch/$USER/firmware-testing 
cd /scratch/$USER/firmware-testing

wget https://go.dev/dl/go1.22.4.linux-amd64.tar.gz 
tar -C $HOME -xzf go1.22.4.linux-amd64.tar.gz 
export PATH=$PATH:$HOME/go/bin

curl -LO https://github.com/oras-project/oras/releases/download/v1.2.0/oras_1.2.0_linux_amd64.tar.gz
mkdir -p $HOME/bin
tar -zxf oras_1.2.0_linux_amd64.tar.gz -C $HOME/bin oras
export PATH=$PATH:$HOME/bin

```

### Step 2: OCI Registry and Artifact Staging

The standard static file server is replaced with a local OCI distribution registry. The `oras push` command mathematically hashes the payload and stores it by its digest. The `127.0.0.1` address is used to force IPv4 routing.

```bash
podman run -d -p 5000:5000 --replace --name local-oci-registry registry:2

curl -O http://rgw-vip.hmn:8080/fw-update/2d64752c1cad11f1aeaa62a6103f192d/NC-1.10.2-22-s.tar.gz

oras push 127.0.0.1:5000/firmware/cray-bmc:1.10.2 \
  --plain-http \
  --artifact-type application/vnd.openchami.firmware.bundle.v1+json \
  NC-1.10.2-22-s.tar.gz:application/vnd.openchami.firmware.payload.v1

```

### Step 3: Service Startup and Job Execution

The Fabrica service is started in the background. The job is submitted utilizing the `"component": "BMC"` parameter to trigger hardware auto-discovery.

```bash
go run ./cmd/server serve --port 8090 --database-url="file:hpc_test.db?cache=shared&_fk=1" &

curl -sS -X POST http://127.0.0.1:8090/firmwareupdatejobs/ \
  -H 'Content-Type: application/json' \
  -d '{
    "metadata": {"name": "live-cray-auto-bmc"},
    "spec": {
      "targetAddress": "x9000c3s7b1",
      "username": "root",
      "password": "initial0",
      "ociReference": "127.0.0.1:5000/firmware/cray-bmc:1.10.2",
      "serverProxyAddress": "10.254.1.20",
      "component": "BMC"
    }
  }'

```

### Step 4: Hardware Validation

Query the BMC to confirm it has accepted the proxy stream and transitioned to the `Updating` state.

```bash
curl -k -u root:initial0 https://x9000c3s7b1/redfish/v1/UpdateService/FirmwareInventory/BMC | jq

```

**Expected Output:**

```json
  % Total    % Received % Xferd  Average Speed   Time    Time     Time  Current
                                 Dload  Upload   Total   Spent    Left  Speed
100   392  100   392    0     0    922      0 --:--:-- --:--:-- --:--:--   924
{
  "@odata.etag": "W/\"1781212049\"",
  "@odata.id": "/redfish/v1/UpdateService/FirmwareInventory/BMC",
  "@odata.type": "#SoftwareInventory.v1_1_0.SoftwareInventory",
  "Description": "Baseboard Management Controller",
  "Id": "BMC",
  "Name": "BMC",
  "SoftwareId": "nc:*:*:*",
  "Status": {
    "Health": "OK",
    "State": "Updating"
  },
  "Updateable": true,
  "Version": "nc.1.10.2-22-shasta-release.arm.2026-01-15T01:13:10+00:00.a0bcef9"
}

```

## 4. Engineering and Network Constraints

* **Asynchronous API Contracts:** The API returns an immediate `201 Created` code when a job is posted. Actual auto-discovery, resolution, and dispatch occur on background processing threads. Status is evaluated by polling the job resource.
* **TLS Policy Constraints:** Outbound communication to target hardware addresses defaults to HTTPS using insecure validation configurations (`InsecureSkipVerify: true`). This accommodation allows connection to self-signed TLS certificates generated by bare-metal controllers.
* **Proxy Address Accuracy:** The `serverProxyAddress` property must be accurately routable from the isolated management VLAN hosting the physical hardware. If this address is misconfigured, the target hardware will time out during the HTTP GET payload pull phase.