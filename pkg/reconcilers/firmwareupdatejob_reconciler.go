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
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	v1 "firmware-manager/apis/hardware.fabrica.dev/v1"

	"oras.land/oras-go/v2/registry/remote/errcode"
)

const (
	firmwareUpdateRetryAttemptsMax = 3
	firmwareUpdateRetryBaseDelay   = 10 * time.Second
)

var (
	getFirmwareBundleByNameFn = getFirmwareBundleByName
	submitSimpleUpdateFn      = submitSimpleUpdate
)

type redfishUpdateResult struct {
	TaskID string
}

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
	if res.Status.JobState == v1.FirmwareUpdateJobStateInProgress ||
		res.Status.JobState == v1.FirmwareUpdateJobStateCompleted ||
		res.Status.JobState == v1.FirmwareUpdateJobStateFailed {
		r.Logger.Debugf("Skipping idempotent FirmwareUpdateJob %s in terminal/active state %s", res.GetUID(), res.Status.JobState)
		return nil
	}

	bundle, err := r.validateFirmwareUpdateJobSpec(ctx, res)
	if err != nil {
		res.Status.JobState = v1.FirmwareUpdateJobStateFailed
		res.Status.ErrorDetail = err.Error()
		return nil
	}

	payloadDigest := strings.TrimSpace(bundle.Status.ExtractedMetadata["payloadDigest"])
	if payloadDigest == "" {
		res.Status.JobState = v1.FirmwareUpdateJobStateFailed
		res.Status.ErrorDetail = fmt.Sprintf("FirmwareBundle %q does not provide status.extractedMetadata.payloadDigest", res.Spec.BundleName)
		return nil
	}

	if res.Status.JobState == "" {
		res.Status.JobState = v1.FirmwareUpdateJobStatePending
	}

	if res.Status.JobState != v1.FirmwareUpdateJobStatePending && res.Status.JobState != v1.FirmwareUpdateJobStateValidating {
		res.Status.JobState = v1.FirmwareUpdateJobStateValidating
	}

	for attempt := 1; attempt <= firmwareUpdateRetryAttemptsMax; attempt++ {
		updateResult, err := submitSimpleUpdateFn(ctx, res, payloadDigest)
		if err == nil {
			res.Status.JobState = v1.FirmwareUpdateJobStateInProgress
			res.Status.TaskID = updateResult.TaskID
			res.Status.ErrorDetail = ""
			r.Logger.Infof("FirmwareUpdateJob %s transitioned to InProgress", res.GetUID())
			return nil
		}

		class, msg := classifyFirmwareUpdateError(err)
		if class == retryClassTransient {
			res.Status.JobState = v1.FirmwareUpdateJobStateValidating
			res.Status.ErrorDetail = appendStatusError(res.Status.ErrorDetail, msg)
			if attempt < firmwareUpdateRetryAttemptsMax {
				delay := computeExponentialBackoff(firmwareUpdateRetryBaseDelay, attempt)
				r.Logger.Warnf("FirmwareUpdateJob %s transient BMC error (attempt %d/%d): %s", res.GetUID(), attempt, firmwareUpdateRetryAttemptsMax, msg)
				if err := sleepWithContextFn(ctx, delay); err != nil {
					res.Status.ErrorDetail = appendStatusError(res.Status.ErrorDetail, err.Error())
					return nil
				}
				continue
			}
			return nil
		}

		res.Status.JobState = v1.FirmwareUpdateJobStateFailed
		res.Status.ErrorDetail = msg
		return nil
	}

	return nil
}

func (r *FirmwareUpdateJobReconciler) validateFirmwareUpdateJobSpec(ctx context.Context, res *v1.FirmwareUpdateJob) (*v1.FirmwareBundle, error) {
	if strings.TrimSpace(res.Spec.TargetAddress) == "" {
		return nil, fmt.Errorf("spec.targetAddress is required")
	}
	if strings.TrimSpace(res.Spec.Username) == "" {
		return nil, fmt.Errorf("spec.username is required")
	}
	if strings.TrimSpace(res.Spec.Password) == "" {
		return nil, fmt.Errorf("spec.password is required")
	}
	if strings.TrimSpace(res.Spec.BundleName) == "" {
		return nil, fmt.Errorf("spec.bundleName is required")
	}
	if strings.TrimSpace(res.Spec.ServerProxyAddress) == "" {
		return nil, fmt.Errorf("spec.serverProxyAddress is required")
	}
	if len(res.Spec.Targets) == 0 {
		return nil, fmt.Errorf("spec.targets must contain at least one value")
	}
	for i, target := range res.Spec.Targets {
		if strings.TrimSpace(target) == "" {
			return nil, fmt.Errorf("spec.targets[%d] must not be empty", i)
		}
	}

	bundle, err := getFirmwareBundleByNameFn(ctx, r.Client, res.Spec.BundleName)
	if err != nil {
		return nil, err
	}

	return bundle, nil
}

func getFirmwareBundleByName(ctx context.Context, client interface {
	List(context.Context, string) ([]interface{}, error)
}, bundleName string) (*v1.FirmwareBundle, error) {
	bundles, err := client.List(ctx, "FirmwareBundle")
	if err != nil {
		return nil, fmt.Errorf("failed to list FirmwareBundle resources: %w", err)
	}

	for _, item := range bundles {
		bundle, ok := item.(*v1.FirmwareBundle)
		if !ok {
			continue
		}
		if bundle.Metadata.Name == bundleName {
			return bundle, nil
		}
	}

	return nil, fmt.Errorf("spec.bundleName %q does not reference an existing FirmwareBundle", bundleName)
}

func submitSimpleUpdate(ctx context.Context, res *v1.FirmwareUpdateJob, payloadDigest string) (*redfishUpdateResult, error) {
	proxyURI := fmt.Sprintf("http://%s:8090/firmware-proxy/layer/%s", strings.TrimSpace(res.Spec.ServerProxyAddress), payloadDigest)
	bmcURL := fmt.Sprintf("https://%s/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate", strings.TrimSpace(res.Spec.TargetAddress))

	body := map[string]interface{}{
		"ImageURI": proxyURI,
		"Targets":  res.Spec.Targets,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Redfish request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, bmcURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to build Redfish request: %w", err)
	}
	req.SetBasicAuth(strings.TrimSpace(res.Spec.Username), strings.TrimSpace(res.Spec.Password))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec
		Timeout:   20 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusServiceUnavailable {
		return nil, fmt.Errorf("BMC service unavailable (503): %s", strings.TrimSpace(string(respBody)))
	}
	if resp.StatusCode == http.StatusBadRequest {
		return nil, fmt.Errorf("redfish bad request (400): %s", strings.TrimSpace(string(respBody)))
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("redfish request failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	taskID := extractTaskID(resp, respBody)
	return &redfishUpdateResult{TaskID: taskID}, nil
}

func classifyFirmwareUpdateError(err error) (retryClass, string) {
	if err == nil {
		return retryClassNone, ""
	}

	errMsg := err.Error()
	if strings.Contains(errMsg, "(503)") {
		return retryClassTransient, errMsg
	}

	var responseErr *errcode.ErrorResponse
	if errors.As(err, &responseErr) {
		if responseErr.StatusCode == http.StatusServiceUnavailable {
			return retryClassTransient, errMsg
		}
		return retryClassTerminal, errMsg
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		var netErr net.Error
		if errors.As(urlErr, &netErr) {
			if netErr.Timeout() {
				return retryClassTransient, errMsg
			}
			return retryClassTerminal, "target host unreachable: " + errMsg
		}
		return retryClassTerminal, "target host unreachable: " + errMsg
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return retryClassTransient, errMsg
		}
		return retryClassTerminal, "target host unreachable: " + errMsg
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return retryClassTransient, errMsg
	}

	if strings.Contains(errMsg, "(400)") || strings.Contains(errMsg, "bad request") {
		return retryClassTerminal, errMsg
	}

	return retryClassTerminal, errMsg
}

func extractTaskID(resp *http.Response, body []byte) string {
	for _, key := range []string{"X-Task-ID", "OData-TaskId", "Location"} {
		value := strings.TrimSpace(resp.Header.Get(key))
		if value == "" {
			continue
		}
		if key == "Location" {
			segments := strings.Split(strings.TrimRight(value, "/"), "/")
			if len(segments) > 0 {
				return segments[len(segments)-1]
			}
		}
		return value
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}

	for _, key := range []string{"TaskID", "taskId", "Id", "id", "@odata.id"} {
		if raw, ok := payload[key]; ok {
			value := strings.TrimSpace(fmt.Sprintf("%v", raw))
			if value == "" {
				continue
			}
			if key == "@odata.id" {
				segments := strings.Split(strings.TrimRight(value, "/"), "/")
				if len(segments) > 0 {
					return segments[len(segments)-1]
				}
			}
			return value
		}
	}

	return ""
}
