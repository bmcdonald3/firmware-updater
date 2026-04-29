#!/bin/bash

# Ensure required tools are installed
command -v jq >/dev/null 2>&1 || { echo >&2 "jq is required but not installed. Aborting."; exit 1; }
command -v openssl >/dev/null 2>&1 || { echo >&2 "openssl is required but not installed. Aborting."; exit 1; }

echo "=========================================="
echo "1. Environment & Mock Setup"
echo "=========================================="

# Create local firmware directory and dummy payload
mkdir -p ./firmware_payloads
echo "dummy binary data" > ./firmware_payloads/test-firmware.bin

# Patch reconciler for local testing (uses cross-platform sed with .bak extension)
sed -i.bak 's/172.23.0.1/127.0.0.1/g' pkg/reconcilers/updatejob_reconciler.go
sed -i.bak 's|/var/www/firmware|./firmware_payloads|g' pkg/reconcilers/updatejob_reconciler.go

# Generate ephemeral SSL certs for the mock BMC
openssl req -x509 -newkey rsa:4096 -nodes -out cert.pem -keyout key.pem -days 1 -subj '/CN=localhost' 2>/dev/null

# Create a lightweight Python HTTPS server to mock the BMC endpoints
cat << 'EOF' > mock_bmc.py
import http.server, ssl, sys

class MockBMC(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        # Accept the payload and return 202 Accepted
        self.send_response(202)
        self.send_header('Content-type', 'application/json')
        self.end_headers()
        self.wfile.write(b'{"status": "Accepted"}')
        print(f"Mock BMC received POST on {self.path}")

    def log_message(self, format, *args):
        pass # Suppress standard python HTTP logs to keep output clean

server_address = ('127.0.0.1', 8443)
httpd = http.server.HTTPServer(server_address, MockBMC)
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ctx.load_cert_chain(certfile='cert.pem', keyfile='key.pem')
httpd.socket = ctx.wrap_socket(httpd.socket, server_side=True)
print("Mock BMC listening on https://127.0.0.1:8443")
httpd.serve_forever()
EOF

# Start the mock BMC in the background
python3 mock_bmc.py &
MOCK_PID=$!

echo "Starting Fabrica server..."
go run ./cmd/server serve --port=8085 --database-url="file:data.db?cache=shared&_fk=1" > fabrica.log 2>&1 &
FABRICA_PID=$!

# Cleanup function to kill background processes on exit
cleanup() {
    echo "Cleaning up background processes..."
    kill $FABRICA_PID $MOCK_PID 2>/dev/null
    rm -f cert.pem key.pem mock_bmc.py ./firmware_payloads/test-firmware.bin response.json
    rm -f pkg/reconcilers/*.bak
}
trap cleanup EXIT

# Dynamically wait for Fabrica to start
echo "Waiting for Fabrica API to come online..."
for i in {1..20}; do
    if curl -s -o /dev/null http://localhost:8085/updatejobs; then
        echo "Fabrica server is up!"
        break
    fi
    sleep 2
    if [ "$i" -eq 20 ]; then
        echo "Timeout waiting for Fabrica server to start. Check fabrica.log:"
        cat fabrica.log
        exit 1
    fi
done

echo "=========================================="
echo "2. Submitting Desired State (Phase A)"
echo "=========================================="

set +e # Disable exit on error to manually handle curl status
HTTP_STATUS=$(curl -s -w "%{http_code}" -o response.json -X POST http://localhost:8085/updatejobs \
  -H "Content-Type: application/json" \
  -d '{
    "apiVersion": "example.fabrica.dev/v1",
    "kind": "UpdateJob",
    "metadata": {
      "name": "pull-update-test"
    },
    "spec": {
      "bmcAddress": "127.0.0.1:8443",
      "username": "root",
      "password": "password123",
      "firmwareFilename": "test-firmware.bin",
      "updateStrategy": "Pull"
    }
  }')
set -e # Re-enable exit on error

if [ "$HTTP_STATUS" -ne 201 ]; then
    echo "Failed to create resource. HTTP Status: $HTTP_STATUS"
    if [ -f response.json ]; then
        cat response.json | jq .
    fi
    exit 1
fi

# Extract the UID of the newly created resource
RESOURCE_UID=$(jq -r '.metadata.uid' response.json)
echo "Successfully created UpdateJob. UID: $RESOURCE_UID"

echo "=========================================="
echo "3. Polling for Reconciler Execution (Phase B)"
echo "=========================================="

MAX_RETRIES=10
ATTEMPT=1
PHASE="Pending"

while [ "$ATTEMPT" -le "$MAX_RETRIES" ]; do
    echo "Polling attempt $ATTEMPT..."
    
    # Fetch the current state of the resource
    STATUS_RESPONSE=$(curl -s http://localhost:8085/updatejobs/"$RESOURCE_UID")
    PHASE=$(echo "$STATUS_RESPONSE" | jq -r '.status.phase // "Pending"')
    MESSAGE=$(echo "$STATUS_RESPONSE" | jq -r '.status.message // "No message yet"')
    
    echo "Current Phase: $PHASE"
    
    if [ "$PHASE" = "Complete" ] || [ "$PHASE" = "Error" ]; then
        echo "Terminal state reached!"
        echo "Final Message: $MESSAGE"
        break
    fi
    
    sleep 2
    ATTEMPT=$((ATTEMPT + 1))
done

if [ "$PHASE" != "Complete" ] && [ "$PHASE" != "Error" ]; then
    echo "Timeout waiting for terminal state. Current phase: $PHASE"
    exit 1
fi

echo "Validation workflow completed successfully."