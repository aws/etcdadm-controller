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
	etcdv1 "github.com/mrajashree/etcdadm-controller/api/v1beta1"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/tools/record"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/collections"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/cluster-api/util/predicates"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// EtcdadmClusterReconciler reconciles a EtcdadmCluster object
type EtcdadmClusterReconciler struct {
	controller controller.Controller
	client.Client
	recorder              record.EventRecorder
	uncachedClient        client.Reader
	Log                   logr.Logger
	Scheme                *runtime.Scheme
	etcdHealthCheckConfig etcdHealthCheckConfig
}

func (r *EtcdadmClusterReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager, done <-chan struct{}) error {
	c, err := ctrl.NewControllerManagedBy(mgr).
		For(&etcdv1.EtcdadmCluster{}).
		Owns(&clusterv1.Machine{}).
		WithEventFilter(predicates.ResourceNotPaused(r.Log)).
		Build(r)

	if err != nil {
		return errors.Wrap(err, "failed setting up with a controller manager")
	}

	err = c.Watch(
		&source.Kind{Type: &clusterv1.Cluster{}},
		handler.EnqueueRequestsFromMapFunc(r.ClusterToEtcdadmCluster),
		predicates.ClusterUnpausedAndInfrastructureReady(r.Log),
	)
	if err != nil {
		return errors.Wrap(err, "failed adding Watch for Clusters to controller manager")
	}

	r.controller = c
	r.recorder = mgr.GetEventRecorderFor("etcdadm-cluster-controller")
	r.uncachedClient = mgr.GetAPIReader()

	go r.startHealthCheckLoop(ctx, done)
	return nil
}

// +kubebuilder:rbac:groups=etcdcluster.cluster.x-k8s.io,resources=etcdadmclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=etcdcluster.cluster.x-k8s.io,resources=etcdadmclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters;clusters/status,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines;machines/status,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=bootstrap.cluster.x-k8s.io,resources=etcdadmconfigs;etcdadmconfigs/status,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps;events;secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=*,verbs=get;list;watch;create;update;patch;delete

func (r *EtcdadmClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, reterr error) {
	log := r.Log.WithValues("etcdadmcluster", req.NamespacedName)

	// Lookup the etcdadm cluster object
	etcdCluster := &etcdv1.EtcdadmCluster{}
	if err := r.Client.Get(ctx, req.NamespacedName, etcdCluster); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get etcdadm cluster")
		return ctrl.Result{}, err
	}

	// Fetch the CAPI Cluster.
	cluster, err := util.GetOwnerCluster(ctx, r.Client, etcdCluster.ObjectMeta)
	if err != nil {
		log.Error(err, "Failed to retrieve owner Cluster from the API Server")
		return ctrl.Result{}, err
	}
	if cluster == nil {
		log.Info("Cluster Controller has not yet set OwnerRef on etcd")
		return ctrl.Result{}, nil
	}
	if !cluster.Status.InfrastructureReady {
		log.Info("Infrastructure cluster is not yet ready")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	if annotations.IsPaused(cluster, etcdCluster) {
		log.Info("Reconciliation is paused for this object")
		return ctrl.Result{}, nil
	}

	// Initialize the patch helper.
	patchHelper, err := patch.NewHelper(etcdCluster, r.Client)
	if err != nil {
		log.Error(err, "Failed to configure the patch helper")
		return ctrl.Result{Requeue: true}, nil
	}

	// Add finalizer first if it does not exist to avoid the race condition between init and delete
	if !controllerutil.ContainsFinalizer(etcdCluster, etcdv1.EtcdadmClusterFinalizer) {
		controllerutil.AddFinalizer(etcdCluster, etcdv1.EtcdadmClusterFinalizer)

		// patch and return right away instead of reusing the main defer,
		// because the main defer may take too much time to get cluster status
		patchOpts := []patch.Option{patch.WithStatusObservedGeneration{}}
		if err := patchHelper.Patch(ctx, etcdCluster, patchOpts...); err != nil {
			log.Error(err, "Failed to patch EtcdadmCluster to add finalizer")
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	defer func() {
		// Always attempt to update status.
		if err := r.updateStatus(ctx, etcdCluster, cluster); err != nil {
			log.Error(err, "Failed to update EtcdadmCluster Status")
			reterr = kerrors.NewAggregate([]error{reterr, err})
		}

		if conditions.IsFalse(etcdCluster, etcdv1.EtcdMachinesSpecUpToDateCondition) &&
			conditions.GetReason(etcdCluster, etcdv1.EtcdMachinesSpecUpToDateCondition) == etcdv1.EtcdRollingUpdateInProgressReason {
			// set ready to false, so that CAPI cluster controller will pause KCP so it doesn't keep checking if endpoints are updated
			etcdCluster.Status.Ready = false
		}

		// Always attempt to Patch the EtcdadmCluster object and status after each reconciliation.
		if err := patchEtcdCluster(ctx, patchHelper, etcdCluster); err != nil {
			log.Error(err, "Failed to patch EtcdadmCluster")
			reterr = kerrors.NewAggregate([]error{reterr, err})
		}

		if reterr == nil && !res.Requeue && !(res.RequeueAfter > 0) && etcdCluster.ObjectMeta.DeletionTimestamp.IsZero() {
			if !etcdCluster.Status.Ready {
				res = ctrl.Result{RequeueAfter: 20 * time.Second}
			}
		}
	}()

	if !etcdCluster.ObjectMeta.DeletionTimestamp.IsZero() {
		// Handle deletion reconciliation loop.
		return r.reconcileDelete(ctx, etcdCluster, cluster)
	}

	return r.reconcile(ctx, etcdCluster, cluster)
}

func (r *EtcdadmClusterReconciler) reconcile(ctx context.Context, etcdCluster *etcdv1.EtcdadmCluster, cluster *clusterv1.Cluster) (ctrl.Result, error) {
	log := r.Log.WithName(etcdCluster.Name)
	var desiredReplicas int

	// Reconcile the external infrastructure reference.
	if err := r.reconcileExternalReference(ctx, cluster, etcdCluster.Spec.InfrastructureTemplate); err != nil {
		return ctrl.Result{}, err
	}

	etcdMachines, err := collections.GetFilteredMachinesForCluster(ctx, r.uncachedClient, cluster, EtcdClusterMachines(cluster.Name, etcdCluster.Name))
	if err != nil {
		return ctrl.Result{}, errors.Wrap(err, "Error filtering machines for etcd cluster")
	}

	ownedMachines := etcdMachines.Filter(collections.OwnedMachines(etcdCluster))

	ep, err := NewEtcdPlane(ctx, r.Client, cluster, etcdCluster, ownedMachines)
	if err != nil {
		return ctrl.Result{}, errors.Wrap(err, "Error initializing internal object EtcdPlane")
	}

	if len(ownedMachines) != len(etcdMachines) {
		if conditions.IsUnknown(etcdCluster, etcdv1.EtcdClusterHasNoOutdatedMembersCondition) || conditions.IsTrue(etcdCluster, etcdv1.EtcdClusterHasNoOutdatedMembersCondition) {
			conditions.MarkFalse(etcdCluster, etcdv1.EtcdClusterHasNoOutdatedMembersCondition, etcdv1.EtcdClusterHasOutdatedMembersReason, clusterv1.ConditionSeverityInfo, "%d etcd members have outdated spec", len(etcdMachines.Difference(ownedMachines)))
		}
		/* These would be the out-of-date etcd machines still belonging to the current etcd cluster as etcd members, but not owned by the EtcdadmCluster object
		When upgrading a cluster, etcd machines need to be upgraded first so that the new etcd endpoints become available. But the outdated controlplane machines
		will keep trying to connect to the etcd members they were configured with. So we cannot delete these older etcd members till controlplane rollout has finished.
		So this is only possible after an upgrade, and these machines can be deleted only after controlplane upgrade has finished. */

		if _, ok := etcdCluster.Annotations[clusterv1.ControlPlaneUpgradeCompletedAnnotation]; ok {
			outdatedMachines := etcdMachines.Difference(ownedMachines)
			log.Info(fmt.Sprintf("Controlplane upgrade has completed, deleting older outdated etcd members: %v", outdatedMachines.Names()))
			for _, outdatedMachine := range outdatedMachines {
				outdatedMachineAddress := getEtcdMachineAddress(outdatedMachine)
				if err := r.removeEtcdMachine(ctx, etcdCluster, cluster, outdatedMachine, outdatedMachineAddress); err != nil {
					return ctrl.Result{}, err
				}
			}
			// requeue so controller reconciles after last machine is deleted and the "EtcdClusterHasNoOutdatedMembersCondition" is marked true
			return ctrl.Result{Requeue: true}, nil
		}
	} else {
		if _, ok := etcdCluster.Annotations[clusterv1.ControlPlaneUpgradeCompletedAnnotation]; ok {
			log.Info("Outdated etcd members deleted, removing controlplane-upgrade complete annotation")
			delete(etcdCluster.Annotations, clusterv1.ControlPlaneUpgradeCompletedAnnotation)
		}
		if conditions.IsFalse(etcdCluster, etcdv1.EtcdClusterHasNoOutdatedMembersCondition) {
			log.Info(fmt.Sprintf("Outdated etcd members deleted, setting %s to true", etcdv1.EtcdClusterHasNoOutdatedMembersCondition))
			conditions.MarkTrue(etcdCluster, etcdv1.EtcdClusterHasNoOutdatedMembersCondition)
		}
	}

	// This aggregates the state of all machines
	conditions.SetAggregate(etcdCluster, etcdv1.EtcdMachinesReadyCondition, ownedMachines.ConditionGetters(), conditions.AddSourceRef(), conditions.WithStepCounterIf(false))

	numCurrentMachines := len(ownedMachines)
	desiredReplicas = int(*etcdCluster.Spec.Replicas)

	// Etcd machines rollout due to configuration changes (e.g. upgrades) takes precedence over other operations.
	needRollout := ep.MachinesNeedingRollout()
	switch {
	case len(needRollout) > 0:
		log.Info("Rolling out Etcd machines", "needRollout", needRollout.Names())
		if conditions.IsFalse(ep.EC, etcdv1.EtcdMachinesSpecUpToDateCondition) && len(ep.UpToDateMachines()) > 0 {
			// update is already in progress, some machines have been rolled out with the new spec
			newestUpToDateMachine := ep.NewestUpToDateMachine()
			newestUpToDateMachineCreationTime := newestUpToDateMachine.CreationTimestamp.Time
			nextMachineUpdateTime := newestUpToDateMachineCreationTime.Add(time.Duration(minEtcdMemberReadySeconds) * time.Second)
			if nextMachineUpdateTime.After(time.Now()) {
				// the latest machine with updated spec should get more time for etcd data sync
				// requeue this after
				after := nextMachineUpdateTime.Sub(time.Now())
				log.Info(fmt.Sprintf("Requeueing etcdadm cluster for updating next machine after %s", after.String()))
				return ctrl.Result{RequeueAfter: after}, nil
			}
			// otherwise, if the minimum time to wait between successive machine updates has passed,
			// check that the latest etcd member is ready
			address := getEtcdMachineAddress(newestUpToDateMachine)
			if address == "" {
				return ctrl.Result{}, nil
			}
			// if member passes healthcheck, that is proof that data sync happened and we can proceed further with upgrade
			if err := r.performEndpointHealthCheck(ctx, cluster, getMemberClientURL(address), true); err != nil {
				return ctrl.Result{}, err
			}
		}
		conditions.MarkFalse(ep.EC, etcdv1.EtcdMachinesSpecUpToDateCondition, etcdv1.EtcdRollingUpdateInProgressReason, clusterv1.ConditionSeverityWarning, "Rolling %d replicas with outdated spec (%d replicas up to date)", len(needRollout), len(ep.Machines)-len(needRollout))
		return r.upgradeEtcdCluster(ctx, cluster, etcdCluster, ep, needRollout)
	default:
		// make sure last upgrade operation is marked as completed.
		// NOTE: we are checking the condition already exists in order to avoid to set this condition at the first
		// reconciliation/before a rolling upgrade actually starts.
		if conditions.Has(ep.EC, etcdv1.EtcdMachinesSpecUpToDateCondition) {
			conditions.MarkTrue(ep.EC, etcdv1.EtcdMachinesSpecUpToDateCondition)

			if _, hasUpgradeAnnotation := etcdCluster.Annotations[etcdv1.UpgradeInProgressAnnotation]; hasUpgradeAnnotation {
				delete(etcdCluster.Annotations, etcdv1.UpgradeInProgressAnnotation)
			}
		}
	}

	switch {
	case numCurrentMachines < desiredReplicas && numCurrentMachines == 0:
		// Create first etcd machine to run etcdadm init
		log.Info("Initializing etcd cluster", "Desired", desiredReplicas, "Existing", numCurrentMachines)
		conditions.MarkFalse(etcdCluster, etcdv1.InitializedCondition, etcdv1.WaitingForEtcdadmInitReason, clusterv1.ConditionSeverityInfo, "")
		conditions.MarkFalse(etcdCluster, etcdv1.EtcdEndpointsAvailable, etcdv1.WaitingForEtcdadmEndpointsToPassHealthcheckReason, clusterv1.ConditionSeverityInfo, "")
		return r.intializeEtcdCluster(ctx, etcdCluster, cluster, ep)
	case numCurrentMachines > 0 && conditions.IsFalse(etcdCluster, etcdv1.InitializedCondition):
		// as soon as first etcd machine is up, etcdadm init would be run on it to initialize the etcd cluster, update the condition
		if !etcdCluster.Status.Initialized {
			// defer func in Reconcile will requeue it after 20 sec
			return ctrl.Result{}, nil
		}
		// since etcd cluster has been initialized
		conditions.MarkTrue(etcdCluster, etcdv1.InitializedCondition)
	case numCurrentMachines < desiredReplicas && numCurrentMachines > 0:
		log.Info("Scaling up etcd cluster", "Desired", desiredReplicas, "Existing", numCurrentMachines)
		return r.scaleUpEtcdCluster(ctx, etcdCluster, cluster, ep)
	case numCurrentMachines > desiredReplicas:
		log.Info("Scaling down etcd cluster", "Desired", desiredReplicas, "Existing", numCurrentMachines)
		// The last parameter corresponds to Machines that need to be rolled out, eg during upgrade, should always be empty here.
		return r.scaleDownEtcdCluster(ctx, etcdCluster, cluster, ep, collections.Machines{})
	}

	return ctrl.Result{}, nil
}

func (r *EtcdadmClusterReconciler) reconcileDelete(ctx context.Context, etcdCluster *etcdv1.EtcdadmCluster, cluster *clusterv1.Cluster) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx, "cluster", cluster.Name)
	log.Info("Reconcile EtcdadmCluster deletion")

	etcdMachines, err := collections.GetFilteredMachinesForCluster(ctx, r.uncachedClient, cluster, EtcdClusterMachines(cluster.Name, etcdCluster.Name))
	if err != nil {
		return ctrl.Result{}, errors.Wrap(err, "Error filtering machines for etcd cluster")
	}

	ownedMachines := etcdMachines.Filter(collections.OwnedMachines(etcdCluster))

	if len(ownedMachines) == 0 {
		// If no etcd machines are left, remove the finalizer
		controllerutil.RemoveFinalizer(etcdCluster, etcdv1.EtcdadmClusterFinalizer)
		return ctrl.Result{}, nil
	}

	// This aggregates the state of all machines
	conditions.SetAggregate(etcdCluster, etcdv1.EtcdMachinesReadyCondition, ownedMachines.ConditionGetters(), conditions.AddSourceRef(), conditions.WithStepCounterIf(false))

	// Delete etcd machines
	machinesToDelete := ownedMachines.Filter(collections.Not(collections.HasDeletionTimestamp))
	var errs []error
	for _, m := range machinesToDelete {
		logger := log.WithValues("machine", m)
		if err := r.Client.Delete(ctx, m); err != nil && !apierrors.IsNotFound(err) {
			logger.Error(err, "Failed to cleanup owned machine")
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		err := kerrors.NewAggregate(errs)
		r.recorder.Eventf(etcdCluster, corev1.EventTypeWarning, "FailedDelete",
			"Failed to delete etcd Machines for cluster %s/%s: %v", cluster.Namespace, cluster.Name, err)
		return ctrl.Result{}, err
	}
	conditions.MarkFalse(etcdCluster, etcdv1.EtcdClusterResizeCompleted, clusterv1.DeletingReason, clusterv1.ConditionSeverityInfo, "")
	// requeue to check if machines are deleted and remove the finalizer
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
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
	// SetSummary sets the Ready condition on an object, in this case the EtcdadmCluster as an aggregate of all conditions defined on EtcdadmCluster
	conditions.SetSummary(ec,
		conditions.WithConditions(
			etcdv1.EtcdMachinesSpecUpToDateCondition,
			etcdv1.EtcdCertificatesAvailableCondition,
			etcdv1.EtcdMachinesReadyCondition,
			etcdv1.EtcdClusterResizeCompleted,
			etcdv1.InitializedCondition,
			etcdv1.EtcdClusterHasNoOutdatedMembersCondition,
			etcdv1.EtcdEndpointsAvailable,
		),
	)

	// patch the EtcdadmCluster conditions based on current values at the end of every reconcile
	return patchHelper.Patch(
		ctx,
		ec,
		patch.WithOwnedConditions{Conditions: []clusterv1.ConditionType{
			clusterv1.ReadyCondition,
			etcdv1.EtcdMachinesSpecUpToDateCondition,
			etcdv1.EtcdCertificatesAvailableCondition,
			etcdv1.EtcdMachinesReadyCondition,
			etcdv1.EtcdClusterResizeCompleted,
			etcdv1.InitializedCondition,
			etcdv1.EtcdClusterHasNoOutdatedMembersCondition,
			etcdv1.EtcdEndpointsAvailable,
		}},
		patch.WithStatusObservedGeneration{},
	)
}
