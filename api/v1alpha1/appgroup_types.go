// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package v1alpha1

import (
	helmopv1 "github.com/fluxcd/helm-operator/pkg/apis/helm.fluxcd.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ApplicationSpec defines the desired state of Application
type ApplicationSpec struct {
	// Namespace to which the HelmRelease object will be deployed
	Namespace string `json:"namespace,omitempty"`
	Subcharts []DAG  `json:"subcharts,omitempty"`
	GroupID   string `json:"groupID,omitempty"`
	// ChartRepoNickname is used to lookup the repository config in the registries config map
	ChartRepoNickname string `json:"repo,omitempty"`
	// XXX (nitishm) **IMPORTANT**: DO NOT USE HelmReleaseSpec.Values!!!
	// ApplicationSpec.Overlays field replaces HelmReleaseSpec.Values field.
	// Setting the HelmReleaseSpec.Values field will not reflect in the deployed Application object
	//
	// Explanation
	// ===========
	// HelmValues uses a map[string]interface{} structure for holding helm values Data.
	// kubebuilder prunes the field value when deploying the Application resource as it considers the field to be an
	// Unknown field. HelmOperator v1 being in maintenance mode, we do not expect them to merge PRs
	// to add the  +kubebuilder:pruning:PreserveUnknownFields
	// https://github.com/fluxcd/helm-operator/issues/585

	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:XPreserveUnknownFields
	Overlays helmopv1.HelmValues `json:"overlays,omitempty"`

	// RepoPath provides the subdir path to the actual chart artifact within a Helm Registry
	// Artifactory for instance utilizes folders to store charts
	RepoPath string `json:"repoPath,omitempty"`

	DisableWait bool `json:"disableWait,omitempty"`

	helmopv1.HelmReleaseSpec `json:",inline"`
}

// ChartStatus denotes the current status of the Application Reconciliation
type ChartStatus struct {
	Phase   helmopv1.HelmReleasePhase `json:"phase,omitempty"`
	Error   string                    `json:"error,omitempty"`
	Version string                    `json:"version,omitempty"`
	Staged  bool                      `json:"staged,omitempty"`
}

// ApplicationGroupSpec defines the desired state of ApplicationGroup
type ApplicationGroupSpec struct {
	Applications []Application `json:"applications,omitempty"`
}

type Application struct {
	DAG  `json:",inline"`
	Spec ApplicationSpec `json:"spec,omitempty"`
}
type DAG struct {
	Name         string   `json:"name,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
}

type ApplicationStatus struct {
	Name        string `json:"name"`
	ChartStatus `json:",inline"`
	Subcharts   map[string]ChartStatus `json:"subcharts,omitempty"`
}

type ReconciliationPhase string

const (
	Init      ReconciliationPhase = "Init"
	Running   ReconciliationPhase = "Running"
	Succeeded ReconciliationPhase = "Succeeded"
	Error     ReconciliationPhase = "Error"
	Rollback  ReconciliationPhase = "Rollback"
)

// ApplicationGroupStatus defines the observed state of ApplicationGroup
type ApplicationGroupStatus struct {
	Checksums    map[string]string   `json:"checksums,omitempty"`
	Applications []ApplicationStatus `json:"status,omitempty"`
	Phase        ReconciliationPhase `json:"phase,omitempty"`
	Update       bool                `json:"update,omitempty"`
	Error        string              `json:"error,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=applicationgroups,scope=Cluster
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=`.metadata.creationTimestamp`
// ApplicationGroup is the Schema for the applicationgroups API
type ApplicationGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ApplicationGroupSpec   `json:"spec,omitempty"`
	Status ApplicationGroupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ApplicationGroupList contains a list of ApplicationGroup
type ApplicationGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ApplicationGroup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ApplicationGroup{}, &ApplicationGroupList{})
}
