package controllers

import (
	"context"
	"fmt"
	"strings"

	etcdv1 "github.com/mrajashree/etcdadm-controller/api/v1alpha3"
	"github.com/pkg/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/collections"
	"sigs.k8s.io/cluster-api/util/conditions"
)

func (r *EtcdadmClusterReconciler) updateStatus(ctx context.Context, ec *etcdv1.EtcdadmCluster, cluster *clusterv1.Cluster) error {
	log := r.Log.WithName(ec.Name)
	selector := EtcdMachinesSelectorForCluster(cluster.Name, ec.Name)
	// Copy label selector to its status counterpart in string format.
	// This is necessary for CRDs including scale subresources.
	ec.Status.Selector = selector.String()

	var etcdMachines collections.FilterableMachineCollection
	var err error
	if conditions.IsFalse(ec, etcdv1.EtcdMachinesSpecUpToDateCondition) {
		// During upgrade with current logic, outdated machines don't get deleted right away.
		// the controller removes their etcdadmCluster ownerRef and updates the Machine. So using uncachedClient here will fetch those changes
		etcdMachines, err = collections.GetMachinesForCluster(ctx, r.uncachedClient, util.ObjectKey(cluster), EtcdClusterMachines(cluster.Name, ec.Name))
	} else {
		etcdMachines, err = collections.GetMachinesForCluster(ctx, r.Client, util.ObjectKey(cluster), EtcdClusterMachines(cluster.Name, ec.Name))
	}
	if err != nil {
		return errors.Wrap(err, "Error filtering machines for etcd cluster")
	}
	ownedMachines := etcdMachines.Filter(collections.OwnedMachines(ec))

	desiredReplicas := *ec.Spec.Replicas
	ec.Status.ReadyReplicas = int32(len(ownedMachines))

	if !ec.DeletionTimestamp.IsZero() {
		return nil
	}

	readyReplicas := ec.Status.ReadyReplicas

	switch {
	case readyReplicas < desiredReplicas:
		conditions.MarkFalse(ec, etcdv1.EtcdClusterResizeCompleted, etcdv1.EtcdScaleUpInProgressReason, clusterv1.ConditionSeverityWarning, "Scaling up etcd cluster to %d replicas (actual %d)", desiredReplicas, readyReplicas)
	case readyReplicas > desiredReplicas:
		conditions.MarkFalse(ec, etcdv1.EtcdClusterResizeCompleted, etcdv1.EtcdScaleDownInProgressReason, clusterv1.ConditionSeverityWarning, "Scaling up etcd cluster to %d replicas (actual %d)", desiredReplicas, readyReplicas)
	default:
		if readyReplicas == desiredReplicas {
			conditions.MarkTrue(ec, etcdv1.EtcdClusterResizeCompleted)
		}
	}

	if readyReplicas == desiredReplicas {
		var endpoints string
		for _, m := range ownedMachines {
			log.Info(fmt.Sprintf("Checking if machine %v has address set for healthcheck", m.Name))
			if len(m.Status.Addresses) == 0 {
				return nil
			}
			if endpoints != "" {
				endpoints += ","
			}
			currentEndpoint := getMemberClientURL(getEtcdMachineAddress(m))
			endpoints += currentEndpoint
		}
		log.Info(fmt.Sprintf("Running healthcheck on endpoints %v", endpoints))
		for _, endpoint := range strings.Split(endpoints, ",") {
			if err := r.performEndpointHealthCheck(ctx, cluster, endpoint); err != nil {
				ec.Status.Ready = false
				return err
			}
		}
		// etcd ready when all machines have address set
		ec.Status.Ready = true
		ec.Status.Endpoints = endpoints
	}
	return nil
}
