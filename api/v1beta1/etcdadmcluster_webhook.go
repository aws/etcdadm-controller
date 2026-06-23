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
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// log is for logging in this package.
var etcdadmclusterlog = logf.Log.WithName("etcdadmcluster-resource")

func (r *EtcdadmCluster) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, r).
		WithDefaulter(&EtcdadmClusterDefaulter{}).
		WithValidator(&EtcdadmClusterValidator{}).
		Complete()
}

// +kubebuilder:webhook:verbs=create;update,path=/mutate-etcdcluster-cluster-x-k8s-io-v1beta1-etcdadmcluster,mutating=true,failurePolicy=fail,groups=etcdcluster.cluster.x-k8s.io,resources=etcdadmclusters,versions=v1beta1,name=metcdadmcluster.kb.io,sideEffects=None,admissionReviewVersions=v1;v1beta1

// +kubebuilder:webhook:verbs=create;update,path=/validate-etcdcluster-cluster-x-k8s-io-v1beta1-etcdadmcluster,mutating=false,failurePolicy=fail,groups=etcdcluster.cluster.x-k8s.io,resources=etcdadmclusters,versions=v1beta1,name=vetcdadmcluster.kb.io,sideEffects=None,admissionReviewVersions=v1;v1beta1

type EtcdadmClusterDefaulter struct{}

// Default implements webhook.CustomDefaulter so a webhook will be registered for the type
func (d *EtcdadmClusterDefaulter) Default(_ context.Context, obj *EtcdadmCluster) error {
	etcdadmclusterlog.Info("default", "name", obj.Name)

	if obj.Spec.Replicas == nil {
		replicas := int32(1)
		obj.Spec.Replicas = &replicas
	}

	if obj.Spec.InfrastructureTemplate.Namespace == "" {
		obj.Spec.InfrastructureTemplate.Namespace = obj.Namespace
	}

	return nil
}

type EtcdadmClusterValidator struct{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type
func (v *EtcdadmClusterValidator) ValidateCreate(_ context.Context, obj *EtcdadmCluster) (admission.Warnings, error) {
	etcdadmclusterlog.Info("validate create", "name", obj.Name)

	allErrs := obj.validateCommon()
	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(GroupVersion.WithKind("EtcdadmCluster").GroupKind(), obj.Name, allErrs)
	}
	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type
func (v *EtcdadmClusterValidator) ValidateUpdate(_ context.Context, oldObj, newObj *EtcdadmCluster) (admission.Warnings, error) {
	etcdadmclusterlog.Info("validate update", "name", newObj.Name)

	allErrs := newObj.validateCommon()
	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(GroupVersion.WithKind("EtcdadmCluster").GroupKind(), newObj.Name, allErrs)
	}
	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type
func (v *EtcdadmClusterValidator) ValidateDelete(_ context.Context, obj *EtcdadmCluster) (admission.Warnings, error) {
	etcdadmclusterlog.Info("validate delete", "name", obj.Name)
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
