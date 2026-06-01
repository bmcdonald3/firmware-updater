// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT
// This file contains user-customizable reconciliation logic for FirmwareBundle.
//
// ⚠️ This file is safe to edit - it will NOT be overwritten by code generation.
package reconcilers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	v1 "firmware-manager/apis/hardware.fabrica.dev/v1"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/errcode"
)

const (
	firmwareBundleArtifactType     = "application/vnd.openchami.firmware.bundle.v1+json"
	firmwareBundleRetryAttemptsMax = 5
	firmwareBundleRetryBaseDelay   = 5 * time.Second
)

var firmwareBundleDiscoverFn = discoverFirmwareBundle
var sleepWithContextFn = sleepWithContext

type bundleDiscoveryResult struct {
	ManifestDigest string
	Annotations    map[string]string
	PayloadDigest  string
}

type retryClass int

const (
	retryClassNone retryClass = iota
	retryClassTransient
	retryClassTerminal
)

// reconcileFirmwareBundle contains custom reconciliation logic.
//
// This method is called by the generated Reconcile() orchestration method.
// Implement FirmwareBundle-specific reconciliation logic here.
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
//   - res: The FirmwareBundle resource to reconcile
//
// Returns:
//   - error: If reconciliation failed (will trigger retry with backoff)
func (r *FirmwareBundleReconciler) reconcileFirmwareBundle(ctx context.Context, res *v1.FirmwareBundle) error {
	if err := v1.ValidateRegistryURLFormat(res.Spec.RegistryURL); err != nil {
		res.Status.Discovered = false
		res.Status.Error = err.Error()
		return nil
	}
	if err := v1.ValidateRepositoryFormat(res.Spec.Repository); err != nil {
		res.Status.Discovered = false
		res.Status.Error = err.Error()
		return nil
	}
	if err := v1.ValidateTagOrDigestFormat(res.Spec.TagOrDigest); err != nil {
		res.Status.Discovered = false
		res.Status.Error = err.Error()
		return nil
	}

	var (
		lastTransient string
		result        *bundleDiscoveryResult
	)

	for attempt := 1; attempt <= firmwareBundleRetryAttemptsMax; attempt++ {
		discovery, err := firmwareBundleDiscoverFn(ctx, res)
		if err == nil {
			result = discovery
			break
		}

		class, msg := classifyRegistryError(err)
		if class == retryClassTransient {
			res.Status.Discovered = false
			lastTransient = appendStatusError(lastTransient, msg)
			if attempt < firmwareBundleRetryAttemptsMax {
				delay := computeExponentialBackoff(firmwareBundleRetryBaseDelay, attempt)
				r.Logger.Warnf("FirmwareBundle %s transient registry error (attempt %d/%d): %s", res.GetUID(), attempt, firmwareBundleRetryAttemptsMax, msg)
				if err := sleepWithContextFn(ctx, delay); err != nil {
					res.Status.Error = appendStatusError(lastTransient, err.Error())
					return nil
				}
				continue
			}
			res.Status.Error = lastTransient
			return nil
		}

		res.Status.Discovered = false
		res.Status.Error = msg
		r.Logger.Warnf("FirmwareBundle %s terminal registry error: %s", res.GetUID(), msg)
		return nil
	}

	if result == nil {
		res.Status.Discovered = false
		res.Status.Error = appendStatusError(lastTransient, "firmware bundle discovery failed")
		return nil
	}

	metadata := make(map[string]string, len(result.Annotations)+1)
	for k, v := range result.Annotations {
		metadata[k] = v
	}
	if result.PayloadDigest != "" {
		metadata["payloadDigest"] = result.PayloadDigest
	}

	res.Status.Discovered = true
	res.Status.ManifestDigest = result.ManifestDigest
	res.Status.ExtractedMetadata = metadata
	res.Status.Error = ""

	r.Logger.Infof("FirmwareBundle %s discovered from registry with manifest %s", res.GetUID(), result.ManifestDigest)
	return nil
}

func discoverFirmwareBundle(ctx context.Context, res *v1.FirmwareBundle) (*bundleDiscoveryResult, error) {
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", strings.TrimSpace(res.Spec.RegistryURL), strings.TrimSpace(res.Spec.Repository)))
	if err != nil {
		return nil, err
	}

	manifestDesc, err := repo.Resolve(ctx, strings.TrimSpace(res.Spec.TagOrDigest))
	if err != nil {
		return nil, err
	}

	manifestBytes, err := content.FetchAll(ctx, repo, manifestDesc)
	if err != nil {
		return nil, err
	}

	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse OCI manifest: %w", err)
	}

	if manifest.ArtifactType != firmwareBundleArtifactType {
		return nil, fmt.Errorf("invalid artifact type %q, expected %q", manifest.ArtifactType, firmwareBundleArtifactType)
	}

	annotations := make(map[string]string)
	for k, v := range manifest.Annotations {
		annotations[k] = v
	}

	payloadDigest := ""
	if len(manifest.Layers) > 0 {
		payloadDigest = manifest.Layers[0].Digest.String()
	}

	return &bundleDiscoveryResult{
		ManifestDigest: manifestDesc.Digest.String(),
		Annotations:    annotations,
		PayloadDigest:  payloadDigest,
	}, nil
}

func classifyRegistryError(err error) (retryClass, string) {
	if err == nil {
		return retryClassNone, ""
	}

	var responseErr *errcode.ErrorResponse
	if errors.As(err, &responseErr) {
		switch responseErr.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
			return retryClassTerminal, err.Error()
		case http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return retryClassTransient, err.Error()
		default:
			return retryClassTerminal, err.Error()
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return retryClassTransient, err.Error()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return retryClassTransient, err.Error()
	}

	if strings.Contains(err.Error(), "invalid artifact type") {
		return retryClassTerminal, err.Error()
	}

	return retryClassTerminal, err.Error()
}

func appendStatusError(current string, next string) string {
	current = strings.TrimSpace(current)
	next = strings.TrimSpace(next)
	if next == "" {
		return current
	}
	if current == "" {
		return next
	}
	if strings.Contains(current, next) {
		return current
	}
	return current + "; " + next
}

func computeExponentialBackoff(base time.Duration, attempt int) time.Duration {
	if attempt <= 0 {
		return base
	}
	return base * time.Duration(1<<(attempt-1))
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
