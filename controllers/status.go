package controllers

import (
	"context"
	etcdv1 "github.com/mrajashree/etcdadm-controller/api/v1alpha4"
	"github.com/pkg/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha4"
	"sigs.k8s.io/cluster-api/util/collections"
)

func (r *EtcdClusterReconciler) updateStatus(ctx context.Context, ec *etcdv1.EtcdCluster, cluster *clusterv1.Cluster) error {
	//log := ctrl.LoggerFrom(ctx, "cluster", cluster.Name)

	selector := collections.EtcdPlaneSelectorForCluster(cluster.Name)
	// Copy label selector to its status counterpart in string format.
	// This is necessary for CRDs including scale subresources.
	ec.Status.Selector = selector.String()

	etcdMachines, err := collections.GetFilteredMachinesForCluster(ctx, r.Client, cluster, collections.EtcdClusterMachines(cluster.Name))
	if err != nil {
		return errors.Wrap(err, "Error filtering machines for etcd cluster")
	}
	ownedMachines := etcdMachines.Filter(collections.OwnedMachines(ec))

	desiredReplicas := *ec.Spec.Replicas

	// set basic data that does not require interacting with the workload cluster
	ec.Status.ReadyReplicas = int32(len(ownedMachines))

	// Return early if the deletion timestamp is set, because we don't want to try to connect to the workload cluster
	// and we don't want to report resize condition (because it is set to deleting into reconcile delete).
	if !ec.DeletionTimestamp.IsZero() {
		return nil
	}

	if ec.Status.ReadyReplicas == desiredReplicas {
		for _, m := range ownedMachines {
			if len(m.Status.Addresses) == 0 {
				return nil
			}
		}
		// etcd ready when all machines have address set
		ec.Status.Ready = true
	}
	return nil
}
