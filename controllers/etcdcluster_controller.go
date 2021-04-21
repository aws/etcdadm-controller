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

package controllers

import (
	"context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/storage/names"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	etcdclusterv1alpha4 "github.com/mrajashree/etcdadm-controller/api/v1alpha4"
	etcdbpv1alpha4 "github.com/mrajashree/etcdadm-bootstrap-provider/api/v1alpha4"
)

// EtcdClusterReconciler reconciles a EtcdCluster object
type EtcdClusterReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=etcdcluster.cluster.x-k8s.io,resources=etcdclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=etcdcluster.cluster.x-k8s.io,resources=etcdclusters/status,verbs=get;update;patch

func (r *EtcdClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = r.Log.WithValues("etcdcluster", req.NamespacedName)

	// your logic here
	log := ctrl.LoggerFrom(ctx)

	// Lookup the etcdadm config
	etcdCluster := &etcdclusterv1alpha4.EtcdCluster{}
	if err := r.Client.Get(ctx, req.NamespacedName, etcdCluster); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get etcd cluster")
		return ctrl.Result{}, err
	}

	def := int32(1)

	if etcdCluster.Spec.Replicas == nil {
		etcdCluster.Spec.Replicas = &def
	}

	for i := int32(0); i < *etcdCluster.Spec.Replicas; i++ {
		owner := metav1.OwnerReference{
			APIVersion: etcdclusterv1alpha4.GroupVersion.String(),
			Kind:       "EtcdCluster",
			Name:       etcdCluster.Name,
			UID:        etcdCluster.UID,
		}

		bootstrapConfig := &etcdbpv1alpha4.EtcdadmConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:            names.SimpleNameGenerator.GenerateName(etcdCluster.Name + "-"),
				Namespace:       etcdCluster.Namespace,
				OwnerReferences: []metav1.OwnerReference{owner},
			},
			Spec: etcdCluster.Spec.EtcdadmConfigSpec,
		}

		if err := r.Client.Create(ctx, bootstrapConfig); err != nil {
			return ctrl.Result{}, errors.Wrap(err, "Failed to create etcdadm bootstrap configuration")
		}
	}

	return ctrl.Result{}, nil
}

func (r *EtcdClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&etcdclusterv1alpha4.EtcdCluster{}).
		Complete(r)
}
