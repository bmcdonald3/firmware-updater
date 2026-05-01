// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT
// This file contains user-customizable reconciliation logic for FirmwareImage.
//
// ⚠️ This file is safe to edit - it will NOT be overwritten by code generation.
package reconcilers

import (
	"context"
	"fmt"
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
	// Check if the firmware file exists in the firmware_payloads directory
	firmwareDir := "./firmware_payloads"
	filePath := filepath.Join(firmwareDir, res.Spec.Filename)

	// Check if file exists and is readable
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			res.Status.Verified = false
			res.Status.Error = fmt.Sprintf("File not found: %s", res.Spec.Filename)
			r.Logger.Warnf("FirmwareImage %s: file not found at %s", res.GetName(), filePath)
		} else {
			res.Status.Verified = false
			res.Status.Error = fmt.Sprintf("Failed to verify file: %v", err)
			r.Logger.Warnf("FirmwareImage %s: failed to verify file: %v", res.GetName(), err)
		}
		return nil
	}

	// Verify it's a regular file
	if !fileInfo.Mode().IsRegular() {
		res.Status.Verified = false
		res.Status.Error = "Target is not a regular file"
		r.Logger.Warnf("FirmwareImage %s: target is not a regular file", res.GetName())
		return nil
	}

	// File exists and is valid
	res.Status.Verified = true
	res.Status.Error = ""
	r.Logger.Infof("FirmwareImage %s: verified successfully (file size: %d bytes)", res.GetName(), fileInfo.Size())

	return nil
}
