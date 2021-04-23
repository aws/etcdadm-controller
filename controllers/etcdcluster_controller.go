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
	"fmt"
	etcdbpv1alpha4 "github.com/mrajashree/etcdadm-bootstrap-provider/api/v1alpha4"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/storage/names"
	"sigs.k8s.io/cluster-api/controllers/external"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/collections"
	"sigs.k8s.io/cluster-api/util/patch"
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha4"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	etcdv1 "github.com/mrajashree/etcdadm-controller/api/v1alpha4"
)

// EtcdClusterReconciler reconciles a EtcdCluster object
type EtcdClusterReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=etcdcluster.cluster.x-k8s.io,resources=etcdclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=etcdcluster.cluster.x-k8s.io,resources=etcdclusters/status,verbs=get;update;patch

func (r *EtcdClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, reterr error) {
	_ = r.Log.WithValues("etcdcluster", req.NamespacedName)

	// your logic here
	log := ctrl.LoggerFrom(ctx)

	// Lookup the etcdadm config
	etcdCluster := &etcdv1.EtcdCluster{}
	if err := r.Client.Get(ctx, req.NamespacedName, etcdCluster); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get etcd cluster")
		return ctrl.Result{}, err
	}

	// Fetch the Cluster.
	cluster, err := util.GetOwnerCluster(ctx, r.Client, etcdCluster.ObjectMeta)
	if err != nil {
		log.Error(err, "Failed to retrieve owner Cluster from the API Server")
		return ctrl.Result{}, err
	}
	if cluster == nil {
		log.Info("Cluster Controller has not yet set OwnerRef on etcd")
		return ctrl.Result{}, nil
	}

	// TODO: add paused check

	// Initialize the patch helper.
	patchHelper, err := patch.NewHelper(etcdCluster, r.Client)
	if err != nil {
		log.Error(err, "Failed to configure the patch helper")
		return ctrl.Result{Requeue: true}, nil
	}

	defer func() {
		// Always attempt to update status.
		if err := r.updateStatus(ctx, etcdCluster, cluster); err != nil {
			log.Error(err, "Failed to update EtcdCluster Status")
			reterr = kerrors.NewAggregate([]error{reterr, err})

		}

		// Always attempt to Patch the EtcdCluster object and status after each reconciliation.
		if err := patchEtcdCluster(ctx, patchHelper, etcdCluster); err != nil {
			log.Error(err, "Failed to patch EtcdCluster")
			reterr = kerrors.NewAggregate([]error{reterr, err})
		}

		// TODO: remove this as soon as we have a proper remote cluster cache in place.
		// Make KCP to requeue in case status is not ready, so we can check for node status without waiting for a full resync (by default 10 minutes).
		// Only requeue if we are not going in exponential backoff due to error, or if we are not already re-queueing, or if the object has a deletion timestamp.
		if reterr == nil && !res.Requeue && !(res.RequeueAfter > 0) && etcdCluster.ObjectMeta.DeletionTimestamp.IsZero() {
			if !etcdCluster.Status.Ready {
				res = ctrl.Result{RequeueAfter: 20 * time.Second}
			}
		}
	}()
	return r.reconcile(ctx, etcdCluster, cluster)
}

func (r *EtcdClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&etcdv1.EtcdCluster{}).
		Complete(r)
}

func (r *EtcdClusterReconciler) reconcile(ctx context.Context, etcdCluster *etcdv1.EtcdCluster, cluster *clusterv1.Cluster) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx, "cluster", cluster.Name)
	var desiredReplicas int
	etcdMachines, err := collections.GetFilteredMachinesForCluster(ctx, r.Client, cluster, collections.EtcdClusterMachines(cluster.Name))
	if err != nil {
		return ctrl.Result{}, errors.Wrap(err, "Error filtering machines for etcd cluster")
	}

	ownedMachines := etcdMachines.Filter(collections.OwnedMachines(etcdCluster))

	numCurrentMachines := len(ownedMachines)
	if etcdCluster.Spec.Replicas != nil {
		desiredReplicas = int(*etcdCluster.Spec.Replicas)
	} else {
		desiredReplicas = 1
	}

	switch {
	case numCurrentMachines < desiredReplicas && numCurrentMachines == 0:
		// Create first etcd machine to run etcdadm init
		log.Info("Initializing etcd cluster", "Desired", desiredReplicas, "Existing", numCurrentMachines)
		return r.intializeEtcdCluster(ctx, etcdCluster, cluster)
	case numCurrentMachines < desiredReplicas && numCurrentMachines > 0:
		if !etcdCluster.Status.Initialized {
			// defer func in Reconcile will requeue it after 20 sec
			return ctrl.Result{}, nil
		}
		log.Info("Scaling up etcd cluster", "Desired", desiredReplicas, "Existing", numCurrentMachines)
		return r.scaleUpEtcdCluster(ctx, etcdCluster, cluster)
	}

	return ctrl.Result{}, nil
}

func (r *EtcdClusterReconciler) intializeEtcdCluster(ctx context.Context, ec *etcdv1.EtcdCluster, cluster *clusterv1.Cluster) (ctrl.Result, error) {
	if err := r.generateCAandClientCertSecrets(ctx, cluster, ec); err != nil {
		r.Log.Error(err, "error generating etcd CA certs")
		return ctrl.Result{}, err
	}
	return r.cloneConfigsAndGenerateMachine(ctx, ec, cluster)
}

func (r *EtcdClusterReconciler) scaleUpEtcdCluster(ctx context.Context, ec *etcdv1.EtcdCluster, cluster *clusterv1.Cluster) (ctrl.Result, error) {
	return r.cloneConfigsAndGenerateMachine(ctx, ec, cluster)
}

func (r *EtcdClusterReconciler) cloneConfigsAndGenerateMachine(ctx context.Context, ec *etcdv1.EtcdCluster, cluster *clusterv1.Cluster) (ctrl.Result, error) {
	// Since the cloned resource should eventually have a controller ref for the Machine, we create an
	// OwnerReference here without the Controller field set
	infraCloneOwner := &metav1.OwnerReference{
		APIVersion: etcdv1.GroupVersion.String(),
		Kind:       "EtcdCluster",
		Name:       ec.Name,
		UID:        ec.UID,
	}

	// Clone the infrastructure template
	infraRef, err := external.CloneTemplate(ctx, &external.CloneTemplateInput{
		Client:      r.Client,
		TemplateRef: &ec.Spec.InfrastructureTemplate,
		Namespace:   ec.Namespace,
		OwnerRef:    infraCloneOwner,
		ClusterName: cluster.Name,
		Labels:      EtcdLabelsForCluster(cluster.Name),
	})

	r.Log.Info(fmt.Sprintf("Is infraRef nil?: %v", infraRef == nil))
	if infraRef == nil {
		return ctrl.Result{}, fmt.Errorf("infraRef is nil")
	}

	bootstrapRef, err := r.generateEtcdadmConfig(ctx, ec, cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.generateMachine(ctx, ec, cluster, infraRef, bootstrapRef); err != nil {
		r.Log.Error(err, "Failed to create initial etcd machine")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *EtcdClusterReconciler) generateEtcdadmConfig(ctx context.Context, ec *etcdv1.EtcdCluster, cluster *clusterv1.Cluster) (*corev1.ObjectReference, error) {
	owner := metav1.OwnerReference{
		APIVersion: etcdv1.GroupVersion.String(),
		Kind:       "EtcdCluster",
		Name:       ec.Name,
		UID:        ec.UID,
	}
	bootstrapConfig := &etcdbpv1alpha4.EtcdadmConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:            names.SimpleNameGenerator.GenerateName(ec.Name + "-"),
			Namespace:       ec.Namespace,
			Labels:          EtcdLabelsForCluster(cluster.Name),
			OwnerReferences: []metav1.OwnerReference{owner},
		},
		Spec: ec.Spec.EtcdadmConfigSpec,
	}
	bootstrapRef := &corev1.ObjectReference{
		APIVersion: etcdbpv1alpha4.GroupVersion.String(),
		Kind:       "EtcdadmConfig",
		Name:       bootstrapConfig.GetName(),
		Namespace:  bootstrapConfig.GetNamespace(),
		UID:        bootstrapConfig.GetUID(),
	}

	if err := r.Client.Create(ctx, bootstrapConfig); err != nil {
		return nil, errors.Wrap(err, "Failed to create etcdadm bootstrap configuration")
	}

	return bootstrapRef, nil
}

func (r *EtcdClusterReconciler) generateMachine(ctx context.Context, ec *etcdv1.EtcdCluster, cluster *clusterv1.Cluster, infraRef, bootstrapRef *corev1.ObjectReference) error {
	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.SimpleNameGenerator.GenerateName(ec.Name + "-"),
			Namespace: ec.Namespace,
			Labels:    EtcdLabelsForCluster(cluster.Name),
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(ec, etcdv1.GroupVersion.WithKind("EtcdCluster")),
			},
		},
		Spec: clusterv1.MachineSpec{
			ClusterName:       cluster.Name,
			InfrastructureRef: *infraRef,
			Bootstrap: clusterv1.Bootstrap{
				ConfigRef: bootstrapRef,
			},
		},
	}
	if err := r.Client.Create(ctx, machine); err != nil {
		return errors.Wrap(err, "failed to create machine")
	}
	return nil
}

func patchEtcdCluster(ctx context.Context, patchHelper *patch.Helper, ec *etcdv1.EtcdCluster) error {
	// Always update the readyCondition by summarizing the state of other conditions.
	//conditions.SetSummary(ec,
	//	conditions.WithConditions(
	//		controlplanev1.MachinesCreatedCondition,
	//		controlplanev1.MachinesSpecUpToDateCondition,
	//		controlplanev1.ResizedCondition,
	//		controlplanev1.MachinesReadyCondition,
	//		controlplanev1.AvailableCondition,
	//		controlplanev1.CertificatesAvailableCondition,
	//	),
	//)

	// Patch the object, ignoring conflicts on the conditions owned by this controller.
	return patchHelper.Patch(
		ctx,
		ec,
		patch.WithOwnedConditions{Conditions: []clusterv1.ConditionType{
			//controlplanev1.MachinesCreatedCondition,
			clusterv1.ReadyCondition,
			//controlplanev1.MachinesSpecUpToDateCondition,
			//controlplanev1.ResizedCondition,
			//controlplanev1.MachinesReadyCondition,
			//controlplanev1.AvailableCondition,
			//controlplanev1.CertificatesAvailableCondition,
		}},
		patch.WithStatusObservedGeneration{},
	)
}
