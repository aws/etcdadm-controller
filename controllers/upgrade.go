package controllers

import (
	"context"
	"fmt"

	etcdv1 "github.com/aws/etcdadm-controller/api/v1beta1"
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/collections"
	ctrl "sigs.k8s.io/controller-runtime"
)

const minEtcdMemberReadySeconds = 60

func (r *EtcdadmClusterReconciler) upgradeEtcdCluster(ctx context.Context,
	cluster *clusterv1.Cluster,
	ec *etcdv1.EtcdadmCluster,
	ep *EtcdPlane,
	machinesToUpgrade collections.Machines,
) (ctrl.Result, error) {
	/*In the absence of static DNS A records as etcd cluster endpoints, IP addresses of the etcd machines are used as etcd cluster endpoints.
	During cluster upgrade, etcd machines need to be upgraded first, since the controlplane machines need to know the updated etcd endpoints to pass in
	as etcd-servers flag value to the kube-apiserver. However, the older outdated controlplane machines will still try to connect to the older etcd members.
	Hence for now, scale down will not delete the machine & remove the etcd member. It will only remove the ownerRef of the EtcdadmCluster object from the Machine*/
	log := r.Log
	if *ec.Spec.Replicas == 1 {
		// for single node etcd cluster, scale up first followed by a scale down
		if int32(ep.Machines.Len()) == *ec.Spec.Replicas {
			return r.scaleUpEtcdCluster(ctx, ec, cluster, ep)
		}
		// remove older etcd member's machine from being an ownedMachine
		return ctrl.Result{}, r.removeFromListOfOwnedMachines(ctx, ep, machinesToUpgrade)
	}

	// Under normal circumstances, ep.Machines, which are the etcd machines owned by the etcdadm
	// cluster should never be higher than the specified number of desired replicas, they should
	// be equal at most. However, it's possible that due to stale client caches or even manual
	// updates (where a user re-adds the owner reference to an old etcd machine), an etcdadm cluster
	// might own at this point more machines that the number of desired replicas. In that case,
	// regardless of the reason, we want to remove the owner reference before creating new replicas.
	// If not, the next reconciliation loop will still detect an owned machine out of spec and wil
	// create a new replica, again without removing ownership of the out of spec machine. This
	// causes a loop of new machines being created without a limit.
	if int32(ep.Machines.Len()) >= *ec.Spec.Replicas {
		log.Info("Scaling down etcd cluster")
		return ctrl.Result{}, r.removeFromListOfOwnedMachines(ctx, ep, machinesToUpgrade)
	}
	log.Info("Scaling up etcd cluster")
	return r.scaleUpEtcdCluster(ctx, ec, cluster, ep)
}

func (r *EtcdadmClusterReconciler) removeFromListOfOwnedMachines(ctx context.Context, ep *EtcdPlane,
	machinesToUpgrade collections.Machines) error {
	machineToDelete, err := selectMachineForScaleDown(ep, machinesToUpgrade)
	if err != nil || machineToDelete == nil {
		return errors.Wrap(err, "failed to select machine for scale down")
	}
	r.Log.Info(fmt.Sprintf("Removing member %s from list of owned Etcd machines", machineToDelete.Name))
	// remove the etcd cluster ownerRef so it's no longer considered a machine owned by the etcd cluster
	machineToDelete.OwnerReferences = []metav1.OwnerReference{}
	return r.Client.Update(ctx, machineToDelete)
}
