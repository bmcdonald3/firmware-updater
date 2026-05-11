from http.server import BaseHTTPRequestHandler, HTTPServer
import json, urllib.request

class MockBMC(BaseHTTPRequestHandler):
    def do_POST(self):
        if "SimpleUpdate" in self.path:
            length = int(self.headers.get('Content-Length', 0))
            data = json.loads(self.rfile.read(length))
            uri = data.get('ImageURI')
            
            print(f"\n[MOCK BMC] Received Redfish Update Command!")
            print(f"[MOCK BMC] Target URI: {uri}")
            print(f"[MOCK BMC] Reaching out to Fabrica Service to pull firmware...")
            
            try:
                # Actively download the file to prove the FMS served it
                req = urllib.request.urlopen(uri, timeout=5)
                content = req.read()
                print(f"[MOCK BMC] Success! Downloaded {len(content)} bytes.")
                print(f"[MOCK BMC] Commencing hardware flash...\n")
                
                self.send_response(202) # 202 Accepted
                self.end_headers()
            except Exception as e:
                print(f"[MOCK BMC] Download failed: {e}")
                self.send_response(400)
                self.end_headers()
        else:
            self.send_response(404)
            self.end_headers()

print("Mock BMC Listening on port 8000...")
HTTPServer(('0.0.0.0', 8000), MockBMC).serve_forever()
