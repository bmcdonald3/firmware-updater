// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT
// This file contains user-customizable reconciliation logic for FirmwareImage.
//
// ⚠️ This file is safe to edit - it will NOT be overwritten by code generation.
package reconcilers

import (
	"context"
	"os"
	"path/filepath"

	"github.com/user/firmware-manager/apis/hardware.fabrica.dev/v1"
)

// reconcileFirmwareImage contains custom reconciliation logic.
//
// This method is called by the generated Reconcile() orchestration method.
// Implement FirmwareImage-specific reconciliation logic here.
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
//   - res: The FirmwareImage resource to reconcile
//
// Returns:
//   - error: If reconciliation failed (will trigger retry with backoff)
func (r *FirmwareImageReconciler) reconcileFirmwareImage(ctx context.Context, res *v1.FirmwareImage) error {
	// Validate firmware image file exists in firmware_payloads directory
	firmwarePath := filepath.Join("firmware_payloads", res.Spec.Filename)
	
	_, err := os.Stat(firmwarePath)
	
	if err != nil {
		if os.IsNotExist(err) {
			res.Status.Verified = false
			res.Status.Error = "firmware file not found: " + firmwarePath
			r.Logger.Warnf("FirmwareImage %s: file not found at %s", res.Metadata.Name, firmwarePath)
		} else {
			res.Status.Verified = false
			res.Status.Error = "failed to stat firmware file: " + err.Error()
			r.Logger.Errorf("FirmwareImage %s: stat error: %v", res.Metadata.Name, err)
		}
	} else {
		res.Status.Verified = true
		res.Status.Error = ""
		r.Logger.Infof("FirmwareImage %s: verified successfully", res.Metadata.Name)
	}

	// Update the resource status
	if err := r.Client.Update(ctx, res); err != nil {
		r.Logger.Errorf("FirmwareImage %s: failed to update status: %v", res.Metadata.Name, err)
		return err
	}

	return nil
}
