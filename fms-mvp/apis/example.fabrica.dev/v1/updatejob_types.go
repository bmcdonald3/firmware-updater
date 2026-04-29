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
	Phase          string     `json:"phase" validate:"oneof=Pending Provisioning Complete Error"`
	Message        string     `json:"message,omitempty"`
	CompletionTime *time.Time `json:"completionTime,omitempty"`
}

// Interface compliance methods required by Fabrica routing and storage.
func (r *UpdateJob) GetKind() string { return "UpdateJob" }
func (r *UpdateJob) GetName() string { return r.Metadata.Name }
func (r *UpdateJob) GetUID() string  { return r.Metadata.UID }
func (r *UpdateJob) IsHub() {}
