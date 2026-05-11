#!/bin/bash

# 1. Create a dummy firmware payload so the Library file server has something to host
mkdir -p firmware_payloads
echo "This is a dummy BMC firmware update payload" > firmware_payloads/bmc-update-v2.bin

# 2. Start the service in the background on port 8090
echo "Starting Firmware Management Service on port 8090..."
PORT:=8090 
go build -o temp_demo_server ./cmd/server
./temp_demo_server --port=$PORT serve --database-url="file:data.db?cache=shared&_fk=1" &
SERVER_PID=$!

# Wait a few seconds for the server to start
sleep 5

# 3. Create the FirmwareImage in the Library
echo "Registering Firmware Image..."
curl -s -X POST http://127.0.0.1:$PORT/firmwareimages \
  -H "Content-Type: application/json" \
  -d '{
    "apiVersion": "hardware.fabrica.dev/v1",
    "kind": "FirmwareImage",
    "metadata": {"name": "bmc-v2"},
    "spec": {
      "filename": "bmc-update-v2.bin",
      "version": "2.0.0",
      "targetComponent": "BMC",
      "models": ["Generic-BMC"]
    }
  }' | jq .

echo "Waiting for file validation..."
sleep 2

# 4. Trigger the Update Job on the real BMC
# "targetAddress": "172.24.0.3",
echo "Triggering Redfish Update Job..."
curl -s -X POST http://127.0.0.1:$PORT/firmwareupdatejobs \
  -H "Content-Type: application/json" \
  -d '{
    "apiVersion": "hardware.fabrica.dev/v1",
    "kind": "FirmwareUpdateJob",
    "metadata": {"name": "demo-update-bmc"},
    "spec": {
      "targetAddress": "172.24.0.1:8000",
      "username": "root",
      "password": "initial0",
      "imageName": "bmc-v2",
      "serverAddress": "172.24.0.1",
      "targets": ["/redfish/v1/UpdateService/SoftwareInventory/BMC"]
    }
  }' | jq .

echo "Waiting for Redfish reconciliation..."
sleep 5

# 5. Check the Final Status
echo "Checking Job Status..."
curl -s http://127.0.0.1:$PORT/firmwareupdatejobs | jq .

# Cleanup (optional: kill the server when done)
kill $SERVER_PID