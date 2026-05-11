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
	// Skip if already in progress or completed
	if res.Status.Status == "InProgress" || res.Status.Status == "Completed" || res.Status.Status == "Failed" {
		return nil
	}

	// Retrieve the FirmwareImage resource by listing all and finding by name
	firmwareImagesIface, err := r.Client.List(ctx, "FirmwareImage")
	if err != nil {
		res.Status.Status = "Failed"
		res.Status.Error = fmt.Sprintf("failed to list FirmwareImages: %v", err)
		r.Logger.Errorf("FirmwareUpdateJob %s: %s", res.Metadata.Name, res.Status.Error)
		r.Client.Update(ctx, res)
		return nil
	}

	// Find the matching FirmwareImage by name
	var firmwareImage *v1.FirmwareImage
	for _, img := range firmwareImagesIface {
		fi, ok := img.(*v1.FirmwareImage)
		if ok && fi.Metadata.Name == res.Spec.ImageName {
			firmwareImage = fi
			break
		}
	}

	if firmwareImage == nil {
		res.Status.Status = "Failed"
		res.Status.Error = fmt.Sprintf("FirmwareImage %s not found", res.Spec.ImageName)
		r.Logger.Errorf("FirmwareUpdateJob %s: %s", res.Metadata.Name, res.Status.Error)
		r.Client.Update(ctx, res)
		return nil
	}

	// Construct the image URI
	imageURI := fmt.Sprintf("http://%s:8090/firmware-files/%s", res.Spec.ServerAddress, firmwareImage.Spec.Filename)
	r.Logger.Infof("FirmwareUpdateJob %s: constructed image URI: %s", res.Metadata.Name, imageURI)

	// Prepare the Redfish update payload
	updatePayload := map[string]interface{}{
		"ImageURI": imageURI,
		"Targets":  res.Spec.Targets,
	}

	payloadBytes, err := json.Marshal(updatePayload)
	if err != nil {
		res.Status.Status = "Failed"
		res.Status.Error = fmt.Sprintf("failed to marshal update payload: %v", err)
		r.Logger.Errorf("FirmwareUpdateJob %s: %s", res.Metadata.Name, res.Status.Error)
		r.Client.Update(ctx, res)
		return nil
	}

	// Create HTTP client with TLS verification disabled
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	httpClient := &http.Client{
		Transport: &http.Transport{TLSClientConfig: tlsConfig},
		Timeout:   30 * time.Second,
	}

	// Construct the Redfish endpoint URL
	redfishURL := fmt.Sprintf("http://%s/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate", res.Spec.TargetAddress)
	r.Logger.Infof("FirmwareUpdateJob %s: posting to %s", res.Metadata.Name, redfishURL)

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", redfishURL, bytes.NewReader(payloadBytes))
	if err != nil {
		res.Status.Status = "Failed"
		res.Status.Error = fmt.Sprintf("failed to create request: %v", err)
		r.Logger.Errorf("FirmwareUpdateJob %s: %s", res.Metadata.Name, res.Status.Error)
		r.Client.Update(ctx, res)
		return nil
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(res.Spec.Username, res.Spec.Password)

	// Execute request
	resp, err := httpClient.Do(req)
	if err != nil {
		res.Status.Status = "Failed"
		res.Status.Error = fmt.Sprintf("failed to execute Redfish request: %v", err)
		r.Logger.Errorf("FirmwareUpdateJob %s: %s", res.Metadata.Name, res.Status.Error)
		r.Client.Update(ctx, res)
		return nil
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		res.Status.Status = "Failed"
		res.Status.Error = fmt.Sprintf("failed to read response body: %v", err)
		r.Logger.Errorf("FirmwareUpdateJob %s: %s", res.Metadata.Name, res.Status.Error)
		r.Client.Update(ctx, res)
		return nil
	}

	// Check response status
	if resp.StatusCode == 200 || resp.StatusCode == 202 {
		res.Status.Status = "InProgress"
		res.Status.StartTime = time.Now().Format(time.RFC3339)
		res.Status.Error = ""

		// Try to extract Task ID if provided in response
		var respData map[string]interface{}
		if err := json.Unmarshal(respBody, &respData); err == nil {
			if taskURI, ok := respData["@odata.id"].(string); ok {
				res.Status.TaskID = taskURI
			}
		}

		r.Logger.Infof("FirmwareUpdateJob %s: update job accepted, status: %d", res.Metadata.Name, resp.StatusCode)
	} else {
		res.Status.Status = "Failed"
		res.Status.Error = fmt.Sprintf("Redfish server returned status %d: %s", resp.StatusCode, string(respBody))
		res.Status.EndTime = time.Now().Format(time.RFC3339)
		r.Logger.Errorf("FirmwareUpdateJob %s: %s", res.Metadata.Name, res.Status.Error)
	}

	// Update the resource status
	if err := r.Client.Update(ctx, res); err != nil {
		r.Logger.Errorf("FirmwareUpdateJob %s: failed to update status: %v", res.Metadata.Name, err)
		return err
	}

	return nil
}
