package controllers

import (
	"context"
	"strings"

	"github.com/go-logr/logr"
	etcdv1 "github.com/mrajashree/etcdadm-controller/api/v1beta1"
	"github.com/pkg/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/collections"
	"sigs.k8s.io/cluster-api/util/conditions"
)

func (r *EtcdadmClusterReconciler) updateStatus(ctx context.Context, ec *etcdv1.EtcdadmCluster, cluster *clusterv1.Cluster) error {
	log := r.Log.WithName(ec.Name)
	selector := EtcdMachinesSelectorForCluster(cluster.Name, ec.Name)
	// Copy label selector to its status counterpart in string format.
	// This is necessary for CRDs including scale subresources.
	ec.Status.Selector = selector.String()

	var etcdMachines collections.Machines
	var err error
	if conditions.IsFalse(ec, etcdv1.EtcdMachinesSpecUpToDateCondition) {
		// During upgrade with current logic, outdated machines don't get deleted right away.
		// the controller removes their etcdadmCluster ownerRef and updates the Machine. So using uncachedClient here will fetch those changes
		etcdMachines, err = collections.GetFilteredMachinesForCluster(ctx, r.uncachedClient, cluster, EtcdClusterMachines(cluster.Name, ec.Name))
	} else {
		etcdMachines, err = collections.GetFilteredMachinesForCluster(ctx, r.Client, cluster, EtcdClusterMachines(cluster.Name, ec.Name))
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

	if readyReplicas < desiredReplicas {
		conditions.MarkFalse(ec, etcdv1.EtcdClusterResizeCompleted, etcdv1.EtcdScaleUpInProgressReason, clusterv1.ConditionSeverityWarning, "Scaling up etcd cluster to %d replicas (actual %d)", desiredReplicas, readyReplicas)
		return nil
	}

	if readyReplicas > desiredReplicas {
		conditions.MarkFalse(ec, etcdv1.EtcdClusterResizeCompleted, etcdv1.EtcdScaleDownInProgressReason, clusterv1.ConditionSeverityWarning, "Scaling up etcd cluster to %d replicas (actual %d)", desiredReplicas, readyReplicas)
		return nil
	}

	conditions.MarkTrue(ec, etcdv1.EtcdClusterResizeCompleted)

	endpoints := getMachinesEndpoints(log, ownedMachines)
	if len(endpoints) == 0 {
		return nil
	}

	log.Info("Running healthcheck on machines", "endpoints", endpoints)
	if machinesReady, err := r.performMachinesHealthCheck(ctx, log, endpoints, cluster); err != nil {
		ec.Status.Ready = false
		return err
	} else if !machinesReady {
		return nil
	}

	// etcd ready when all machines have address set
	ec.Status.Ready = true
	ec.Status.Endpoints = strings.Join(endpoints, ",")
	// set creationComplete to true, this is only set once after the first set of endpoints are ready and never unset, to indicate that the cluster has been created
	ec.Status.CreationComplete = true

	return nil
}

func getMachinesEndpoints(log logr.Logger, machines collections.Machines) []string {
	endpoints := make([]string, 0, len(machines))
	for _, m := range machines {
		log.Info("Checking if machine has address set for healthcheck", "machine", m.Name)
		if len(m.Status.Addresses) == 0 {
			log.Info("No address set in machine yet", "machine", m.Name)
			return nil
		}

		currentEndpoint := getMemberClientURL(getEtcdMachineAddress(m))
		endpoints = append(endpoints, currentEndpoint)
	}

	return endpoints
}

func (r *EtcdadmClusterReconciler) performMachinesHealthCheck(ctx context.Context, log logr.Logger, endpoints []string, cluster *clusterv1.Cluster) (healthy bool, err error) {
	for _, endpoint := range endpoints {
		err := r.performEndpointHealthCheck(ctx, cluster, endpoint, true)
		if errors.Is(err, portNotOpenErr) {
			log.Info("Machine is not listening yet, this is probably transient, while etcd starts", "endpoint", endpoint)
			return false, nil
		}

		if err != nil {
			return false, err
		}
	}

	return true, nil
}
