// Copyright © 2025 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package v1

import (
	"context"
	"github.com/openchami/fabrica/pkg/fabrica"
)

// FirmwareImage represents a firmwareimage resource
type FirmwareImage struct {
	APIVersion string           `json:"apiVersion"`
	Kind       string           `json:"kind"`
	Metadata   fabrica.Metadata `json:"metadata"`
	Spec       FirmwareImageSpec   `json:"spec" validate:"required"`
	Status     FirmwareImageStatus `json:"status,omitempty"`
}

// FirmwareImageSpec defines the desired state of FirmwareImage
type FirmwareImageSpec struct {
	Filename       string   `json:"filename" validate:"required"`
	Version        string   `json:"version" validate:"required"`
	TargetComponent string   `json:"targetComponent" validate:"required"`
	Models         []string `json:"models" validate:"required,min=1"`
}

// FirmwareImageStatus defines the observed state of FirmwareImage
type FirmwareImageStatus struct {
	Verified bool   `json:"verified"`
	Error    string `json:"error,omitempty"`
}

// Validate implements custom validation logic for FirmwareImage
func (r *FirmwareImage) Validate(ctx context.Context) error {
	// Add custom validation logic here
	// Example:
	// if r.Spec.Description == "forbidden" {
	//     return errors.New("description 'forbidden' is not allowed")
	// }

	return nil
}
// GetKind returns the kind of the resource
func (r *FirmwareImage) GetKind() string {
	return "FirmwareImage"
}

// GetName returns the name of the resource
func (r *FirmwareImage) GetName() string {
	return r.Metadata.Name
}

// GetUID returns the UID of the resource
func (r *FirmwareImage) GetUID() string {
	return r.Metadata.UID
}

// IsHub marks this as the hub/storage version
func (r *FirmwareImage) IsHub() {}
