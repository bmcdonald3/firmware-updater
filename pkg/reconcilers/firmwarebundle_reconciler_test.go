package reconcilers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	v1 "firmware-manager/apis/hardware.fabrica.dev/v1"

	"github.com/openchami/fabrica/pkg/reconcile"
	"oras.land/oras-go/v2/registry/remote/errcode"
)

func TestReconcileFirmwareBundle(t *testing.T) {
	reconciler := &FirmwareBundleReconciler{
		BaseReconciler: reconcile.BaseReconciler{Logger: reconcile.NewDefaultLogger()},
	}
	originalDiscover := firmwareBundleDiscoverFn
	originalSleep := sleepWithContextFn
	t.Cleanup(func() {
		firmwareBundleDiscoverFn = originalDiscover
		sleepWithContextFn = originalSleep
	})
	sleepWithContextFn = func(ctx context.Context, delay time.Duration) error {
		return nil
	}

	tests := []struct {
		name                 string
		resource             *v1.FirmwareBundle
		discoverFn           func(context.Context, *v1.FirmwareBundle) (*bundleDiscoveryResult, error)
		expectDiscovered     bool
		expectErrorSubstring string
		expectMetadata       bool
	}{
		{
			name: "valid bundle discovers manifest and metadata",
			resource: &v1.FirmwareBundle{
				Metadata: v1.FirmwareBundle{}.Metadata,
				Spec: v1.FirmwareBundleSpec{
					RegistryURL: "registry.example.org",
					Repository:  "firmware/hpe/cray-ex-node-bmc",
					TagOrDigest: "v2.14.7",
				},
			},
			discoverFn: func(ctx context.Context, res *v1.FirmwareBundle) (*bundleDiscoveryResult, error) {
				return &bundleDiscoveryResult{
					ManifestDigest: "sha256:1234",
					Annotations: map[string]string{
						"vendor": "openchami",
					},
					PayloadDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				}, nil
			},
			expectDiscovered: true,
			expectMetadata:   true,
		},
		{
			name: "invalid registry fails validation",
			resource: &v1.FirmwareBundle{
				Spec: v1.FirmwareBundleSpec{
					RegistryURL: "https://registry.example.org",
					Repository:  "firmware/hpe",
					TagOrDigest: "v2.14.7",
				},
			},
			expectDiscovered:     false,
			expectErrorSubstring: "registryURL",
		},
		{
			name: "registry 404 is terminal failure",
			resource: &v1.FirmwareBundle{
				Spec: v1.FirmwareBundleSpec{
					RegistryURL: "registry.example.org",
					Repository:  "firmware/hpe",
					TagOrDigest: "v2.14.7",
				},
			},
			discoverFn: func(ctx context.Context, res *v1.FirmwareBundle) (*bundleDiscoveryResult, error) {
				return nil, &errcode.ErrorResponse{StatusCode: http.StatusNotFound}
			},
			expectDiscovered:     false,
			expectErrorSubstring: "response status code 404",
		},
		{
			name: "registry 503 is transient and appended to error",
			resource: &v1.FirmwareBundle{
				Spec: v1.FirmwareBundleSpec{
					RegistryURL: "registry.example.org",
					Repository:  "firmware/hpe",
					TagOrDigest: "v2.14.7",
				},
			},
			discoverFn: func(ctx context.Context, res *v1.FirmwareBundle) (*bundleDiscoveryResult, error) {
				return nil, &errcode.ErrorResponse{StatusCode: http.StatusServiceUnavailable}
			},
			expectDiscovered:     false,
			expectErrorSubstring: "response status code 503",
		},
		{
			name: "invalid artifact type is terminal failure",
			resource: &v1.FirmwareBundle{
				Spec: v1.FirmwareBundleSpec{
					RegistryURL: "registry.example.org",
					Repository:  "firmware/hpe",
					TagOrDigest: "v2.14.7",
				},
			},
			discoverFn: func(ctx context.Context, res *v1.FirmwareBundle) (*bundleDiscoveryResult, error) {
				return nil, fmt.Errorf("invalid artifact type \"x\", expected \"%s\"", firmwareBundleArtifactType)
			},
			expectDiscovered:     false,
			expectErrorSubstring: "invalid artifact type",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if tt.discoverFn != nil {
				firmwareBundleDiscoverFn = tt.discoverFn
			} else {
				firmwareBundleDiscoverFn = originalDiscover
			}

			err := reconciler.reconcileFirmwareBundle(context.Background(), tt.resource)
			if err != nil {
				t.Fatalf("reconcileFirmwareBundle() returned unexpected error: %v", err)
			}

			if tt.resource.Status.Discovered != tt.expectDiscovered {
				t.Fatalf("expected Discovered=%v, got %v", tt.expectDiscovered, tt.resource.Status.Discovered)
			}

			if tt.expectErrorSubstring == "" && tt.resource.Status.Error != "" {
				t.Fatalf("expected empty status error, got %q", tt.resource.Status.Error)
			}
			if tt.expectErrorSubstring != "" && !strings.Contains(tt.resource.Status.Error, tt.expectErrorSubstring) {
				t.Fatalf("expected status error to contain %q, got %q", tt.expectErrorSubstring, tt.resource.Status.Error)
			}

			if tt.expectMetadata {
				if tt.resource.Status.ManifestDigest == "" {
					t.Fatal("expected manifest digest to be populated")
				}
				if len(tt.resource.Status.ExtractedMetadata) == 0 {
					t.Fatal("expected extracted metadata to be populated")
				}
				if tt.resource.Status.ExtractedMetadata["payloadDigest"] == "" {
					t.Fatal("expected payload digest to be populated")
				}
			}
		})
	}
}

func TestShouldUsePlainHTTPRegistry(t *testing.T) {
	tests := []struct {
		name        string
		registryURL string
		expect      bool
	}{
		{name: "localhost with port", registryURL: "localhost:5000", expect: true},
		{name: "localhost without port", registryURL: "localhost", expect: true},
		{name: "ipv4 loopback", registryURL: "127.0.0.1:5000", expect: true},
		{name: "ipv6 loopback", registryURL: "[::1]:5000", expect: true},
		{name: "remote registry", registryURL: "registry.example.org", expect: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldUsePlainHTTPRegistry(tt.registryURL); got != tt.expect {
				t.Fatalf("shouldUsePlainHTTPRegistry(%q) = %v, want %v", tt.registryURL, got, tt.expect)
			}
		})
	}
}
