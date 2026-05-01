// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT
// This file contains user-customizable reconciliation logic for FirmwareUpdateJob.
//
// ⚠️ This file is safe to edit - it will NOT be overwritten by code generation.
package reconcilers

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/user/firmware-manager/apis/hardware.fabrica.dev/v1"
	"github.com/user/firmware-manager/internal/storage"
)

// reconcileFirmwareUpdateJob contains custom reconciliation logic.
//
// This method is called by the generated Reconcile() orchestration method.
// Implement FirmwareUpdateJob-specific reconciliation logic here.
//
// Guidelines:
//  1. Keep this method idempotent (safe to call multiple times)
//  2. Update Status fields to reflect observed state
//  3. Emit events for significant state changes using r.EmitEvent()
//  4. Use r.Logger for debugging (Infof, Warnf, Errorf, Debugf)
//  5. Return errors for transient failures (will retry with backoff)
//  6. Access storage via r.Client (Get, List, Update, Create, Delete)
//
// Example implementation patterns:
//
// For hardware resources (BMC, Node):
//   - Connect to hardware endpoint
//   - Query current state
//   - Update Status.Connected, Status.Version, Status.Health
//   - Emit events when state changes
//
// For hierarchical resources (Rack, Chassis):
//   - Create/reconcile child resources
//   - Update Status with child counts and references
//   - Emit events when topology changes
//
// Parameters:
//   - ctx: Context for cancellation and timeouts
//   - res: The FirmwareUpdateJob resource to reconcile
//
// Returns:
//   - error: If reconciliation failed (will trigger retry with backoff)
func (r *FirmwareUpdateJobReconciler) reconcileFirmwareUpdateJob(ctx context.Context, res *v1.FirmwareUpdateJob) error {
	// Skip if already completed
	if res.Status.Status != "" && (res.Status.Status == "Success" || res.Status.Status == "Failed") {
		r.Logger.Debugf("FirmwareUpdateJob %s already completed with status: %s", res.GetName(), res.Status.Status)
		return nil
	}

	// 1. Retrieve the FirmwareImage resource by name
	allImages, err := storage.LoadAllFirmwareImages(ctx)
	if err != nil {
		res.Status.Status = "Failed"
		res.Status.Error = fmt.Sprintf("Failed to load FirmwareImages: %v", err)
		r.Logger.Errorf("FirmwareUpdateJob %s: failed to load images: %v", res.GetName(), err)
		return nil
	}

	// Find the image by name
	var firmwareImage *v1.FirmwareImage
	for _, img := range allImages {
		if img.Metadata.Name == res.Spec.ImageName {
			firmwareImage = img
			break
		}
	}

	if firmwareImage == nil {
		res.Status.Status = "Failed"
		res.Status.Error = fmt.Sprintf("FirmwareImage '%s' not found", res.Spec.ImageName)
		r.Logger.Warnf("FirmwareUpdateJob %s: firmware image not found", res.GetName())
		return nil
	}

	// 2. Verify the firmware image
	if !firmwareImage.Status.Verified {
		res.Status.Status = "Failed"
		res.Status.Error = fmt.Sprintf("FirmwareImage %s not verified: %s", res.Spec.ImageName, firmwareImage.Status.Error)
		r.Logger.Warnf("FirmwareUpdateJob %s: firmware image not verified", res.GetName())
		return nil
	}

	// 3. Set start time if not already set
	if res.Status.StartTime == nil {
		now := time.Now()
		res.Status.StartTime = &now
	}

	// 4. Construct the Image URI
	// Using localhost:8090 as per spec for testing
	imageURI := fmt.Sprintf("http://localhost:8090/firmware-files/%s", firmwareImage.Spec.Filename)

	// 5. Execute HTTP POST to Redfish endpoint
	payload := map[string]interface{}{
		"ImageURI":         imageURI,
		"TransferProtocol": "HTTP",
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		res.Status.Status = "Failed"
		res.Status.Error = fmt.Sprintf("Failed to marshal JSON payload: %v", err)
		r.Logger.Errorf("FirmwareUpdateJob %s: failed to marshal payload: %v", res.GetName(), err)
		return nil
	}

	// Create HTTP client with TLS verification disabled
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 30 * time.Second,
	}

	// Construct the Redfish URL
	redfishURL := fmt.Sprintf("https://%s/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate", res.Spec.TargetAddress)

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", redfishURL, bytes.NewBuffer(jsonPayload))
	if err != nil {
		res.Status.Status = "Failed"
		res.Status.Error = fmt.Sprintf("Failed to create HTTP request: %v", err)
		r.Logger.Errorf("FirmwareUpdateJob %s: failed to create request: %v", res.GetName(), err)
		return nil
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(res.Spec.Username, res.Spec.Password)

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		res.Status.Status = "Failed"
		res.Status.Error = fmt.Sprintf("Failed to execute Redfish request: %v", err)
		r.Logger.Errorf("FirmwareUpdateJob %s: failed to execute request: %v", res.GetName(), err)
		return nil
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		res.Status.Status = "Failed"
		res.Status.Error = fmt.Sprintf("Failed to read response: %v", err)
		r.Logger.Errorf("FirmwareUpdateJob %s: failed to read response: %v", res.GetName(), err)
		return nil
	}

	// Check response status
	if resp.StatusCode == 200 || resp.StatusCode == 202 {
		// Success
		res.Status.Status = "InProgress"
		res.Status.Error = ""

		// Try to extract Redfish Task ID from response headers or body
		if taskURL := resp.Header.Get("Location"); taskURL != "" {
			res.Status.RedfishTaskID = taskURL
		}

		// Also try to parse from response body if it contains a task reference
		var respData map[string]interface{}
		if err := json.Unmarshal(respBody, &respData); err == nil {
			if taskRef, ok := respData["@odata.id"]; ok {
				if taskRefStr, ok := taskRef.(string); ok {
					res.Status.RedfishTaskID = taskRefStr
				}
			}
		}

		r.Logger.Infof("FirmwareUpdateJob %s: successfully submitted to BMC %s", res.GetName(), res.Spec.TargetAddress)
	} else {
		// Error
		res.Status.Status = "Failed"
		res.Status.Error = fmt.Sprintf("Redfish API returned status %d: %s", resp.StatusCode, string(respBody))
		r.Logger.Errorf("FirmwareUpdateJob %s: Redfish API error: status=%d, body=%s", res.GetName(), resp.StatusCode, string(respBody))
		now := time.Now()
		res.Status.EndTime = &now
	}

	return nil
}
