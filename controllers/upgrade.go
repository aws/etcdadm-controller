package controllers

import (
	"context"

	etcdv1 "github.com/mrajashree/etcdadm-controller/api/v1alpha3"
	"github.com/mrajashree/etcdadm-controller/util/collections"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"
	ctrl "sigs.k8s.io/controller-runtime"
)

func (r *EtcdadmClusterReconciler) upgradeEtcdCluster(ctx context.Context,
	cluster *clusterv1.Cluster,
	ec *etcdv1.EtcdadmCluster,
	ep *EtcdPlane,
	machinesToUpgrade collections.Machines,
) (ctrl.Result, error) {
	log := r.Log
	if int32(ep.Machines.Len()) == *ec.Spec.Replicas {
		log.Info("Scaling down etcd cluster")
		return r.scaleDownEtcdCluster(ctx, ec, cluster, ep, machinesToUpgrade)
	}
	log.Info("Scaling up etcd cluster")
	return r.scaleUpEtcdCluster(ctx, ec, cluster, ep)
}
