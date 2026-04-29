package reconcilers

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"

	v1 "github.com/example/fms-mvp/apis/example.fabrica.dev/v1"
)

// reconcileUpdateJob handles the concrete operations for the UpdateJob resource.
func (r *UpdateJobReconciler) reconcileUpdateJob(ctx context.Context, res *v1.UpdateJob) error {
	// 1. Idempotency Check
	// If the job has already reached a terminal state, exit immediately.
	if res.Status.Phase == "Complete" || res.Status.Phase == "Error" {
		return nil
	}

	// 2. Progressive Status Update
	// Lock the resource into a provisioning state before initiating the long-running network tasks.
	res.Status.Phase = "Provisioning"
	if err := r.Client.Update(ctx, res); err != nil {
		return fmt.Errorf("failed to update status to Provisioning: %w", err)
	}

	// Configure the HTTP Client to bypass TLS verification for the BMC connection.
	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		Timeout:   15 * time.Minute,
	}

	var statusCode int
	var responseBody []byte
	var reqErr error

	// 3. Execute Concrete Operations
	if res.Spec.UpdateStrategy == "Pull" {
		// PULL STRATEGY: Instruct the BMC to download the file from the global background server.
		targetURL := fmt.Sprintf("https://%s/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate", res.Spec.BMCAddress)
		imageURI := fmt.Sprintf("http://172.23.0.1:8080/%s", res.Spec.FirmwareFilename)

		payload := map[string]interface{}{
			"ImageURI": imageURI,
			"Targets":  []string{}, // Required workaround for Intel Redfish parsers
		}
		bodyBytes, _ := json.Marshal(payload)

		req, err := http.NewRequestWithContext(ctx, "POST", targetURL, bytes.NewBuffer(bodyBytes))
		if err != nil {
			return r.handleError(ctx, res, fmt.Errorf("failed to create Pull request: %w", err))
		}

		req.SetBasicAuth(res.Spec.Username, res.Spec.Password)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			reqErr = err
		} else {
			defer resp.Body.Close()
			statusCode = resp.StatusCode
			responseBody, _ = io.ReadAll(resp.Body)
		}

	} else if res.Spec.UpdateStrategy == "Push" {
		// PUSH STRATEGY: Read the local binary and stream it directly to the BMC.
		targetURL := fmt.Sprintf("https://%s/redfish/v1/UpdateService/MultipartHttpPushUri", res.Spec.BMCAddress)
		filePath := filepath.Join("/var/www/firmware", res.Spec.FirmwareFilename)

		file, err := os.Open(filePath)
		if err != nil {
			return r.handleError(ctx, res, fmt.Errorf("failed to open firmware file at %s: %w", filePath, err))
		}
		defer file.Close()

		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)

		// Append the JSON parameters part
		part, err := writer.CreateFormField("UpdateParameters")
		if err == nil {
			part.Write([]byte(`{"Targets": []}`))
		}

		// Append the binary file part
		filePart, err := writer.CreateFormFile("UpdateFile", res.Spec.FirmwareFilename)
		if err == nil {
			io.Copy(filePart, file)
		}
		writer.Close()

		req, err := http.NewRequestWithContext(ctx, "POST", targetURL, body)
		if err != nil {
			return r.handleError(ctx, res, fmt.Errorf("failed to create Push request: %w", err))
		}

		req.SetBasicAuth(res.Spec.Username, res.Spec.Password)
		req.Header.Set("Content-Type", writer.FormDataContentType())

		resp, err := client.Do(req)
		if err != nil {
			reqErr = err
		} else {
			defer resp.Body.Close()
			statusCode = resp.StatusCode
			responseBody, _ = io.ReadAll(resp.Body)
		}
	} else {
		// Invalid strategy provided in the Spec.
		return r.handleError(ctx, res, fmt.Errorf("unknown update strategy: %s", res.Spec.UpdateStrategy))
	}

	// 4. Update Final Status
	// Handle raw network execution errors.
	if reqErr != nil {
		return r.handleError(ctx, res, fmt.Errorf("network execution failed: %w", reqErr))
	}

	// Evaluate the HTTP response from the BMC.
	if statusCode >= 200 && statusCode < 300 {
		res.Status.Phase = "Complete"
		res.Status.Message = fmt.Sprintf("Update accepted by BMC. HTTP Status: %d", statusCode)
		now := time.Now()
		res.Status.CompletionTime = &now
	} else {
		res.Status.Phase = "Error"
		res.Status.Message = fmt.Sprintf("BMC rejected payload. HTTP Status: %d. Response: %s", statusCode, string(responseBody))
	}

	// Save the final state back to the Fabrica database.
	if err := r.Client.Update(ctx, res); err != nil {
		return fmt.Errorf("failed to update final status: %w", err)
	}

	return nil
}

// handleError is a helper function to set the error state, save it, and return the error to trigger a requeue.
func (r *UpdateJobReconciler) handleError(ctx context.Context, res *v1.UpdateJob, err error) error {
	res.Status.Phase = "Error"
	res.Status.Message = err.Error()
	_ = r.Client.Update(ctx, res)
	return err
}