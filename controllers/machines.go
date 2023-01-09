package controllers

import (
	"context"

	etcdv1 "github.com/aws/etcdadm-controller/api/v1beta1"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/collections"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TODO(g-gaston): remove this once we have a stable CAPI repo that contains this,
// MachineEtcdReadyLabelName is the label set on machines that have succesfully joined the etcd cluster.
const MachineEtcdReadyLabelName = "cluster.x-k8s.io/etcd-ready"

type etcdMachines map[string]etcdMachine

// endpoints returns all the API endpoints for the machines that have one available.
func (e etcdMachines) endpoints() []string {
	endpoints := make([]string, 0, len(e))
	for _, m := range e {
		if m.endpoint != "" {
			endpoints = append(endpoints, m.endpoint)
		}
	}

	return endpoints
}

// etcdMachine represents a Machine that should be a member of an etcd cluster.
type etcdMachine struct {
	*clusterv1.Machine
	endpoint    string
	listening   bool
	healthError error
}

func (e etcdMachine) healthy() bool {
	return e.listening && e.healthError == nil
}

// updateMachinesEtcdReadyLabel adds the etcd-ready label to the machines that have joined the etcd cluster.
func (r *EtcdadmClusterReconciler) updateMachinesEtcdReadyLabel(ctx context.Context, log logr.Logger, machines etcdMachines) error {
	for _, m := range machines {
		if _, ok := m.Labels[MachineEtcdReadyLabelName]; ok {
			continue
		}

		if !m.healthy() {
			continue
		}

		m.Labels[MachineEtcdReadyLabelName] = "true"
		if err := r.Client.Update(ctx, m.Machine); err != nil {
			return errors.Wrapf(err, "adding etcd ready label to machine %s", m.Name)
		}
	}

	return nil
}

// checkOwnedMachines verifies the health of all etcd members.
func (r *EtcdadmClusterReconciler) checkOwnedMachines(ctx context.Context, log logr.Logger, etcdadmCluster *etcdv1.EtcdadmCluster, cluster *clusterv1.Cluster) (etcdMachines, error) {
	ownedMachines, err := r.getCurrentOwnedMachines(ctx, etcdadmCluster, cluster)
	if err != nil {
		return nil, err
	}

	machines := make(etcdMachines, len(ownedMachines))
	for k, machine := range ownedMachines {
		m := etcdMachine{Machine: machine}
		endpoint := getMachineEtcdEndpoint(machine)
		if endpoint == "" {
			machines[k] = m
			continue
		}

		err := r.performEndpointHealthCheck(ctx, cluster, endpoint, true)
		// This is not ideal, performEndpointHealthCheck uses an error to signal both a not ready/unhealthy member
		// and also transient errors when performing such check.
		// Ideally we would separate these 2 so we can abort on error and mark as unhealthy separetly
		m.healthError = err
		if errors.Is(err, portNotOpenErr) {
			log.Info("Machine is not listening yet, this is probably transient, while etcd starts", "endpoint", endpoint)
		} else {
			m.listening = true
		}

		machines[k] = m
	}

	return machines, nil
}

// getCurrentOwnedMachines lists all the owned machines by the etcdadm cluster.
func (r *EtcdadmClusterReconciler) getCurrentOwnedMachines(ctx context.Context, etcdadmCluster *etcdv1.EtcdadmCluster, cluster *clusterv1.Cluster) (collections.Machines, error) {
	var client client.Reader
	if conditions.IsFalse(etcdadmCluster, etcdv1.EtcdMachinesSpecUpToDateCondition) {
		// During upgrade with current logic, outdated machines don't get deleted right away.
		// the controller removes their etcdadmCluster ownerRef and updates the Machine. So using uncachedClient here will fetch those changes
		client = r.uncachedClient
	} else {
		client = r.Client
	}
	etcdMachines, err := collections.GetFilteredMachinesForCluster(ctx, client, cluster, EtcdClusterMachines(cluster.Name, etcdadmCluster.Name))
	if err != nil {
		return nil, errors.Wrap(err, "Error filtering machines for etcd cluster")
	}
	ownedMachines := etcdMachines.Filter(collections.OwnedMachines(etcdadmCluster))

	return ownedMachines, nil
}

// getMachineEtcdEndpoint constructs the full API url for an etcd member Machine.
// If the Machine doesn't have yet the right address, it returns empty string.
func getMachineEtcdEndpoint(machine *clusterv1.Machine) string {
	address := getEtcdMachineAddress(machine)
	if address == "" {
		return ""
	}

	return getMemberClientURL(address)
}
