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
	"time"

	"github.com/go-logr/logr"
	etcdv1 "github.com/mrajashree/etcdadm-controller/api/v1alpha3"
	"github.com/mrajashree/etcdadm-controller/util/collections"
	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/cluster-api/util/predicates"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// EtcdadmClusterReconciler reconciles a EtcdadmCluster object
type EtcdadmClusterReconciler struct {
	controller controller.Controller
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=etcdcluster.cluster.x-k8s.io,resources=etcdadmclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=etcdcluster.cluster.x-k8s.io,resources=etcdadmclusters/status,verbs=get;update;patch

func (r *EtcdadmClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, reterr error) {
	_ = r.Log.WithValues("etcdadmcluster", req.NamespacedName)
	log := ctrl.LoggerFrom(ctx)

	// Lookup the etcdadm cluster object
	etcdCluster := &etcdv1.EtcdadmCluster{}
	if err := r.Client.Get(ctx, req.NamespacedName, etcdCluster); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get etcdadm cluster")
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
			log.Error(err, "Failed to update EtcdadmCluster Status")
			reterr = kerrors.NewAggregate([]error{reterr, err})

		}

		// Always attempt to Patch the EtcdadmCluster object and status after each reconciliation.
		if err := patchEtcdCluster(ctx, patchHelper, etcdCluster); err != nil {
			log.Error(err, "Failed to patch EtcdadmCluster")
			reterr = kerrors.NewAggregate([]error{reterr, err})
		}

		if reterr == nil && !res.Requeue && !(res.RequeueAfter > 0) && etcdCluster.ObjectMeta.DeletionTimestamp.IsZero() {
			if !etcdCluster.Status.CreationComplete {
				res = ctrl.Result{RequeueAfter: 20 * time.Second}
			}
		}
	}()
	return r.reconcile(ctx, etcdCluster, cluster)
}

func (r *EtcdadmClusterReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	c, err := ctrl.NewControllerManagedBy(mgr).
		For(&etcdv1.EtcdadmCluster{}).
		Owns(&clusterv1.Machine{}).
		Build(r)

	if err != nil {
		return errors.Wrap(err, "failed setting up with a controller manager")
	}

	err = c.Watch(
		&source.Kind{Type: &clusterv1.Cluster{}},
		handler.EnqueueRequestsFromMapFunc(r.ClusterToEtcdadmCluster),
		predicates.ClusterUnpausedAndInfrastructureReady(ctrl.LoggerFrom(ctx)),
	)
	if err != nil {
		return errors.Wrap(err, "failed adding Watch for Clusters to controller manager")
	}

	r.controller = c

	return nil
}

func (r *EtcdadmClusterReconciler) reconcile(ctx context.Context, etcdCluster *etcdv1.EtcdadmCluster, cluster *clusterv1.Cluster) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx, "cluster", cluster.Name)
	var desiredReplicas int
	etcdMachines, err := collections.GetFilteredMachinesForCluster(ctx, r.Client, cluster, collections.EtcdClusterMachines(cluster.Name))
	if err != nil {
		return ctrl.Result{}, errors.Wrap(err, "Error filtering machines for etcd cluster")
	}

	ownedMachines := etcdMachines.Filter(collections.OwnedMachines(etcdCluster))

	ep, err := NewEtcdPlane(ctx, r.Client, cluster, etcdCluster, ownedMachines)
	if err != nil {
		return ctrl.Result{}, errors.Wrap(err, "Error initializing internal Etcd Cluster")
	}

	numCurrentMachines := len(ownedMachines)
	if etcdCluster.Spec.Replicas != nil {
		desiredReplicas = int(*etcdCluster.Spec.Replicas)
	} else {
		desiredReplicas = 1
	}

	// Etcd machines rollout due to configuration changes (e.g. upgrades) takes precedence over other operations.
	needRollout := ep.MachinesNeedingRollout()
	switch {
	case len(needRollout) > 0:
		log.Info("Rolling out Etcd machines", "needRollout", needRollout.Names())
		//conditions.MarkFalse(controlPlane.KCP, controlplanev1.MachinesSpecUpToDateCondition, controlplanev1.RollingUpdateInProgressReason, clusterv1.ConditionSeverityWarning, "Rolling %d replicas with outdated spec (%d replicas up to date)", len(needRollout), len(controlPlane.Machines)-len(needRollout))
		return r.upgradeEtcdCluster(ctx, cluster, etcdCluster, ep, needRollout)
	default:
		// make sure last upgrade operation is marked as completed.
		// NOTE: we are checking the condition already exists in order to avoid to set this condition at the first
		// reconciliation/before a rolling upgrade actually starts.
		//if conditions.Has(controlPlane.KCP, controlplanev1.MachinesSpecUpToDateCondition) {
		//	conditions.MarkTrue(controlPlane.KCP, controlplanev1.MachinesSpecUpToDateCondition)
		//}
	}

	switch {
	case numCurrentMachines < desiredReplicas && numCurrentMachines == 0:
		// Create first etcd machine to run etcdadm init
		log.Info("Initializing etcd cluster", "Desired", desiredReplicas, "Existing", numCurrentMachines)
		return r.intializeEtcdCluster(ctx, etcdCluster, cluster, ep)
	case numCurrentMachines < desiredReplicas && numCurrentMachines > 0:
		if !etcdCluster.Status.Initialized {
			// defer func in Reconcile will requeue it after 20 sec
			return ctrl.Result{}, nil
		}
		log.Info("Scaling up etcd cluster", "Desired", desiredReplicas, "Existing", numCurrentMachines)
		return r.scaleUpEtcdCluster(ctx, etcdCluster, cluster, ep)
	case numCurrentMachines > desiredReplicas:
		log.Info("Scaling down etcd cluster", "Desired", desiredReplicas, "Existing", numCurrentMachines)
		// The last parameter corresponds to Machines that need to be rolled out, eg during upgrade, should always be empty here.
		return r.scaleDownEtcdCluster(ctx, etcdCluster, cluster, ep, collections.Machines{})
	}

	return ctrl.Result{}, nil
}

// ClusterToEtcdadmCluster is a handler.ToRequestsFunc to be used to enqueue requests for reconciliation
// for EtcdadmCluster based on updates to a Cluster.
func (r *EtcdadmClusterReconciler) ClusterToEtcdadmCluster(o client.Object) []ctrl.Request {
	c, ok := o.(*clusterv1.Cluster)
	if !ok {
		panic(fmt.Sprintf("Expected a Cluster but got a %T", o))
	}

	etcdRef := c.Spec.ManagedExternalEtcdRef
	if etcdRef != nil && etcdRef.Kind == "EtcdadmCluster" {
		return []ctrl.Request{{NamespacedName: client.ObjectKey{Namespace: etcdRef.Namespace, Name: etcdRef.Name}}}
	}

	return nil
}

func patchEtcdCluster(ctx context.Context, patchHelper *patch.Helper, ec *etcdv1.EtcdadmCluster) error {
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
