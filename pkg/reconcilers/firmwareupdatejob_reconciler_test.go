package reconcilers

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	v1 "firmware-manager/apis/hardware.fabrica.dev/v1"

	"github.com/openchami/fabrica/pkg/reconcile"
)

type fakeClient struct {
	listByKind map[string][]interface{}
	listErr    error
}

func (f *fakeClient) Get(ctx context.Context, kind, uid string) (interface{}, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeClient) List(ctx context.Context, kind string) ([]interface{}, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listByKind[kind], nil
}

func (f *fakeClient) Update(ctx context.Context, resource interface{}) error {
	return nil
}

func (f *fakeClient) Create(ctx context.Context, resource interface{}) error {
	return nil
}

func (f *fakeClient) Delete(ctx context.Context, kind, uid string) error {
	return nil
}

func TestReconcileFirmwareUpdateJob(t *testing.T) {
	validBundle := &v1.FirmwareBundle{}
	validBundle.Metadata.Name = "bundle-1"
	validBundle.Status.ExtractedMetadata = map[string]string{
		"payloadDigest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}

	reconciler := &FirmwareUpdateJobReconciler{
		BaseReconciler: reconcile.BaseReconciler{
			Client: &fakeClient{listByKind: map[string][]interface{}{"FirmwareBundle": {validBundle}}},
			Logger: reconcile.NewDefaultLogger(),
		},
	}

	originalGetBundle := getFirmwareBundleByNameFn
	originalSubmit := submitSimpleUpdateFn
	originalSleep := sleepWithContextFn
	t.Cleanup(func() {
		getFirmwareBundleByNameFn = originalGetBundle
		submitSimpleUpdateFn = originalSubmit
		sleepWithContextFn = originalSleep
	})
	sleepWithContextFn = func(ctx context.Context, delay time.Duration) error {
		return nil
	}

	baseJob := &v1.FirmwareUpdateJob{
		Spec: v1.FirmwareUpdateJobSpec{
			TargetAddress:      "10.0.0.5",
			Username:           "admin",
			Password:           "secret",
			BundleName:         "bundle-1",
			Targets:            []string{"/redfish/v1/UpdateService/FirmwareInventory/BMC"},
			ServerProxyAddress: "127.0.0.1",
		},
	}

	cloneBaseJob := func() *v1.FirmwareUpdateJob {
		j := *baseJob
		j.Spec.Targets = append([]string(nil), baseJob.Spec.Targets...)
		return &j
	}

	tests := []struct {
		name                   string
		job                    *v1.FirmwareUpdateJob
		bundle                 *v1.FirmwareBundle
		submitErr              error
		submitResult           *redfishUpdateResult
		expectState            string
		expectTaskID           string
		expectErrorSubstring   string
		expectErrorDetailEmpty bool
	}{
		{
			name: "pending transitions to in progress on success",
			job: func() *v1.FirmwareUpdateJob {
				j := cloneBaseJob()
				j.Status.JobState = v1.FirmwareUpdateJobStatePending
				return j
			}(),
			bundle: &v1.FirmwareBundle{
				Metadata: v1.FirmwareBundle{}.Metadata,
				Status: v1.FirmwareBundleStatus{ExtractedMetadata: map[string]string{
					"payloadDigest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				}},
			},
			submitResult: &redfishUpdateResult{TaskID: "task-123"},
			expectState:  v1.FirmwareUpdateJobStateInProgress,
			expectTaskID: "task-123",
		},
		{
			name: "terminal state remains unchanged",
			job: func() *v1.FirmwareUpdateJob {
				j := cloneBaseJob()
				j.Status.JobState = v1.FirmwareUpdateJobStateCompleted
				return j
			}(),
			expectState:            v1.FirmwareUpdateJobStateCompleted,
			expectErrorDetailEmpty: true,
		},
		{
			name: "missing targets marks job failed",
			job: func() *v1.FirmwareUpdateJob {
				j := cloneBaseJob()
				j.Spec.Targets = nil
				return j
			}(),
			expectState:          v1.FirmwareUpdateJobStateFailed,
			expectErrorSubstring: "targets",
		},
		{
			name: "unknown bundle marks job failed",
			job: func() *v1.FirmwareUpdateJob {
				j := cloneBaseJob()
				j.Spec.BundleName = "does-not-exist"
				return j
			}(),
			expectState:          v1.FirmwareUpdateJobStateFailed,
			expectErrorSubstring: "does not reference an existing FirmwareBundle",
		},
		{
			name: "missing payload digest marks job failed",
			job:  cloneBaseJob(),
			bundle: &v1.FirmwareBundle{
				Metadata: v1.FirmwareBundle{}.Metadata,
				Status:   v1.FirmwareBundleStatus{ExtractedMetadata: map[string]string{}},
			},
			expectState:          v1.FirmwareUpdateJobStateFailed,
			expectErrorSubstring: "payloadDigest",
		},
		{
			name:                 "bmc 503 keeps job validating",
			job:                  cloneBaseJob(),
			submitErr:            errors.New("BMC service unavailable (503): temporary busy"),
			expectState:          v1.FirmwareUpdateJobStateValidating,
			expectErrorSubstring: "503",
		},
		{
			name:                 "bmc 400 marks job failed",
			job:                  cloneBaseJob(),
			submitErr:            errors.New("redfish bad request (400): invalid target"),
			expectState:          v1.FirmwareUpdateJobStateFailed,
			expectErrorSubstring: "400",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			getFirmwareBundleByNameFn = originalGetBundle
			submitSimpleUpdateFn = originalSubmit

			if tt.bundle != nil {
				tt.bundle.Metadata.Name = "bundle-1"
				bundle := tt.bundle
				getFirmwareBundleByNameFn = func(ctx context.Context, client interface {
					List(context.Context, string) ([]interface{}, error)
				}, bundleName string) (*v1.FirmwareBundle, error) {
					if bundleName != "bundle-1" {
						return nil, errors.New("unexpected bundle lookup")
					}
					return bundle, nil
				}
			}
			if tt.submitErr != nil || tt.submitResult != nil {
				submitErr := tt.submitErr
				submitResult := tt.submitResult
				submitSimpleUpdateFn = func(ctx context.Context, res *v1.FirmwareUpdateJob, payloadDigest string) (*redfishUpdateResult, error) {
					if submitErr != nil {
						return nil, submitErr
					}
					return submitResult, nil
				}
			}

			err := reconciler.reconcileFirmwareUpdateJob(context.Background(), tt.job)
			if err != nil {
				t.Fatalf("reconcileFirmwareUpdateJob() returned unexpected error: %v", err)
			}

			if tt.job.Status.JobState != tt.expectState {
				t.Fatalf("expected state %q, got %q", tt.expectState, tt.job.Status.JobState)
			}
			if tt.expectTaskID != "" && tt.job.Status.TaskID != tt.expectTaskID {
				t.Fatalf("expected task id %q, got %q", tt.expectTaskID, tt.job.Status.TaskID)
			}

			if tt.expectErrorSubstring != "" && !strings.Contains(tt.job.Status.ErrorDetail, tt.expectErrorSubstring) {
				t.Fatalf("expected error detail to contain %q, got %q", tt.expectErrorSubstring, tt.job.Status.ErrorDetail)
			}

			if tt.expectErrorDetailEmpty && tt.job.Status.ErrorDetail != "" {
				t.Fatalf("expected empty error detail, got %q", tt.job.Status.ErrorDetail)
			}
		})
	}
}
