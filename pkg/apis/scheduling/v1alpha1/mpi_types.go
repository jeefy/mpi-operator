/*
Copyright 2018 The Kubernetes authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	arbcorev1 "github.com/kubernetes-sigs/kube-batch/pkg/apis/scheduling/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// MPISpec defines the desired state of MPI
type MPISpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Job      batchv1.Job        `json:"job"`
	PodGroup arbcorev1.PodGroup `json:"podGroup"`
}

// MPIStatus defines the observed state of MPI
type MPIStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// the job status
	// +optional
	Job batchv1.JobStatus `json:"job,omitempty"`
	// the podGroup status
	// +optional
	PodGroup arbcorev1.PodGroupStatus `json:"podGroup,omitempty"`
	// additional job descriptors
	// +optional
	Descriptors map[string]string `json:"descriptors,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// MPI is the Schema for the mpis API
// +k8s:openapi-gen=true
type MPI struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MPISpec   `json:"spec,omitempty"`
	Status MPIStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// MPIList contains a list of MPI
type MPIList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MPI `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MPI{}, &MPIList{}, &arbcorev1.PodGroup{}, &arbcorev1.PodGroupList{})
}
