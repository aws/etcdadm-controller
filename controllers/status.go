package controllers

import (
	"context"
	"sort"
	"strings"

	etcdv1 "github.com/aws/etcdadm-controller/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *EtcdadmClusterReconciler) updateStatus(ctx context.Context, ec *etcdv1.EtcdadmCluster, cluster *clusterv1.Cluster, ownedMachines etcdMachines) error {
	log := r.Log.WithName(ec.Name)
	selector := EtcdMachinesSelectorForCluster(cluster.Name, ec.Name)
	// Copy label selector to its status counterpart in string format.
	// This is necessary for CRDs including scale subresources.
	ec.Status.Selector = selector.String()

	machineNameList := []string{}
	for _, machine := range ownedMachines {
		machineNameList = append(machineNameList, machine.Name)
	}
	log.Info("following machines owned by this etcd cluster", "OwnedMachines", machineNameList)

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

	for _, m := range ownedMachines {
		if !m.healthy() {
			if m.listening {
				// The machine is listening but not ready/unhealthy
				ec.Status.Ready = false
				return m.healthError
			} else {
				// The machine is not listening, probably transient while etcd starts
				return nil
			}
		}
	}

	// etcd ready when all machines have address set
	ec.Status.Ready = true
	conditions.MarkTrue(ec, etcdv1.EtcdEndpointsAvailable)

	endpoints := ownedMachines.endpoints()
	sort.Strings(endpoints)
	currEndpoints := strings.Join(endpoints, ",")

	log.Info("Comparing current and previous endpoints")
	// Checking if endpoints have changed. This avoids unnecessary client calls
	// to get and update the Secret containing the endpoints
	if ec.Status.Endpoints != currEndpoints {
		log.Info("Updating endpoints annotation, and the Secret containing etcdadm join address")
		ec.Status.Endpoints = currEndpoints
		secretNameNs := client.ObjectKey{Name: ec.Status.InitMachineAddress, Namespace: cluster.Namespace}
		secretInitAddress := &corev1.Secret{}
		if err := r.Client.Get(ctx, secretNameNs, secretInitAddress); err != nil {
			return err
		}
		if len(endpoints) > 0 {
			secretInitAddress.Data["address"] = []byte(getEtcdMachineAddressFromClientURL(endpoints[0]))
		} else {
			secretInitAddress.Data["address"] = []byte("")
		}
		secretInitAddress.Data["clientUrls"] = []byte(ec.Status.Endpoints)
		r.Log.Info("Updating init secret with endpoints")
		if err := r.Client.Update(ctx, secretInitAddress); err != nil {
			return err
		}
	}

	// set creationComplete to true, this is only set once after the first set of endpoints are ready and never unset, to indicate that the cluster has been created
	ec.Status.CreationComplete = true

	return nil
}
