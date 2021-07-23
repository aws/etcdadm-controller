package controllers

import (
	"context"

	etcdv1 "github.com/mrajashree/etcdadm-controller/api/v1alpha3"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"
	"sigs.k8s.io/cluster-api/util/collections"
	ctrl "sigs.k8s.io/controller-runtime"
)

const minEtcdMemberReadySeconds = 150

func (r *EtcdadmClusterReconciler) upgradeEtcdCluster(ctx context.Context,
	cluster *clusterv1.Cluster,
	ec *etcdv1.EtcdadmCluster,
	ep *EtcdPlane,
	machinesToUpgrade collections.FilterableMachineCollection,
) (ctrl.Result, error) {
	log := r.Log
	if *ec.Spec.Replicas == 1 {
		// for single node etcd cluster, scale up first followed by a scale down
		if int32(ep.Machines.Len()) == *ec.Spec.Replicas {
			return r.scaleUpEtcdCluster(ctx, ec, cluster, ep)
		}
		return r.scaleDownEtcdCluster(ctx, ec, cluster, ep, machinesToUpgrade)
	}
	if int32(ep.Machines.Len()) == *ec.Spec.Replicas {
		log.Info("Scaling down etcd cluster")
		return r.scaleDownEtcdCluster(ctx, ec, cluster, ep, machinesToUpgrade)
	}
	log.Info("Scaling up etcd cluster")
	return r.scaleUpEtcdCluster(ctx, ec, cluster, ep)
}
