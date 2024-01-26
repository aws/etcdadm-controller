/*


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

package v1beta1

import (
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// log is for logging in this package.
var etcdadmclusterlog = logf.Log.WithName("etcdadmcluster-resource")

func (r *EtcdadmCluster) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// +kubebuilder:webhook:verbs=create;update,path=/mutate-etcdcluster-cluster-x-k8s-io-v1beta1-etcdadmcluster,mutating=true,failurePolicy=fail,groups=etcdcluster.cluster.x-k8s.io,resources=etcdadmclusters,versions=v1beta1,name=metcdadmcluster.kb.io,sideEffects=None,admissionReviewVersions=v1;v1beta1

var _ webhook.Defaulter = &EtcdadmCluster{}

// +kubebuilder:webhook:verbs=create;update,path=/validate-etcdcluster-cluster-x-k8s-io-v1beta1-etcdadmcluster,mutating=false,failurePolicy=fail,groups=etcdcluster.cluster.x-k8s.io,resources=etcdadmclusters,versions=v1beta1,name=vetcdadmcluster.kb.io,sideEffects=None,admissionReviewVersions=v1;v1beta1

var _ webhook.Validator = &EtcdadmCluster{}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (r *EtcdadmCluster) Default() {
	etcdadmclusterlog.Info("default", "name", r.Name)

	if r.Spec.Replicas == nil {
		replicas := int32(1)
		r.Spec.Replicas = &replicas
	}

	if r.Spec.InfrastructureTemplate.Namespace == "" {
		r.Spec.InfrastructureTemplate.Namespace = r.Namespace
	}
}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *EtcdadmCluster) ValidateCreate() (admission.Warnings, error) {
	etcdadmclusterlog.Info("validate create", "name", r.Name)

	allErrs := r.validateCommon()
	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(GroupVersion.WithKind("EtcdadmCluster").GroupKind(), r.Name, allErrs)
	}
	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *EtcdadmCluster) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	etcdadmclusterlog.Info("validate update", "name", r.Name)

	allErrs := r.validateCommon()
	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(GroupVersion.WithKind("EtcdadmCluster").GroupKind(), r.Name, allErrs)
	}
	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *EtcdadmCluster) ValidateDelete() (admission.Warnings, error) {
	etcdadmclusterlog.Info("validate delete", "name", r.Name)

	// TODO(user): fill in your validation logic upon object deletion.
	return nil, nil
}

func (r *EtcdadmCluster) validateCommon() (allErrs field.ErrorList) {
	if r.Spec.Replicas == nil {
		allErrs = append(
			allErrs,
			field.Required(
				field.NewPath("spec", "replicas"),
				"is required",
			),
		)
	} else if *r.Spec.Replicas <= 0 {
		allErrs = append(
			allErrs,
			field.Forbidden(
				field.NewPath("spec", "replicas"),
				"cannot be less than or equal to 0",
			),
		)
	} else if r.Spec.Replicas != nil && *r.Spec.Replicas%2 == 0 {
		allErrs = append(
			allErrs,
			field.Forbidden(
				field.NewPath("spec", "replicas"),
				"etcd cluster cannot have an even number of nodes",
			),
		)
	}

	if r.Spec.InfrastructureTemplate.Namespace != r.Namespace {
		allErrs = append(
			allErrs,
			field.Invalid(
				field.NewPath("spec", "infrastructureTemplate", "namespace"),
				r.Spec.InfrastructureTemplate.Namespace,
				"must match metadata.namespace",
			),
		)
	}

	return allErrs
}
