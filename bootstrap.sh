#!/bin/bash
set -euo pipefail

PROJECT_NAME="fms-mvp"
MODULE_NAME="github.com/example/fms-mvp"
GROUP="example.fabrica.dev"
API_VERSION="v1"
API_DIR="apis/$GROUP/$API_VERSION"

echo "Cleaning up existing project directory if it exists..."
rm -rf $PROJECT_NAME

echo "Initializing Fabrica project with reconciliation enabled..."
fabrica init $PROJECT_NAME --module $MODULE_NAME --group $GROUP --storage-type ent --db sqlite --events --events-bus memory --reconcile

cd $PROJECT_NAME

echo "Adding UpdateJob resource..."
fabrica add resource UpdateJob

echo "Injecting custom declarative schema for UpdateJob..."
cat << 'EOF' > $API_DIR/updatejob_types.go
package v1

import (
	"time"

	"github.com/openchami/fabrica/pkg/fabrica"
)

// UpdateJob represents the complete resource wrapper expected by Fabrica.
type UpdateJob struct {
	APIVersion string           `json:"apiVersion" validate:"required"`
	Kind       string           `json:"kind" validate:"required"`
	Metadata   fabrica.Metadata `json:"metadata"`
	Spec       UpdateJobSpec    `json:"spec"`
	Status     UpdateJobStatus  `json:"status,omitempty"`
}

// UpdateJobSpec defines the desired state of the firmware update provided by the user.
type UpdateJobSpec struct {
	BMCAddress       string `json:"bmcAddress" validate:"required"`
	Username         string `json:"username" validate:"required"`
	Password         string `json:"password" validate:"required"`
	FirmwareFilename string `json:"firmwareFilename" validate:"required"`
	UpdateStrategy   string `json:"updateStrategy" validate:"oneof=Push Pull"`
}

// UpdateJobStatus defines the observed state of the firmware update managed by the reconciler.
type UpdateJobStatus struct {
	Phase          string     `json:"phase,omitempty`
	Message        string     `json:"message,omitempty"`
	CompletionTime *time.Time `json:"completionTime,omitempty"`
}

// Interface compliance methods required by Fabrica routing and storage.
func (r *UpdateJob) GetKind() string { return "UpdateJob" }
func (r *UpdateJob) GetName() string { return r.Metadata.Name }
func (r *UpdateJob) GetUID() string  { return r.Metadata.UID }
func (r *UpdateJob) IsHub() {}
EOF

echo "Executing Fabrica code generation..."
fabrica generate

echo "Resolving Go dependencies..."
go mod tidy

echo "Bootstrap complete. The environment is ready for the reconciler implementation."