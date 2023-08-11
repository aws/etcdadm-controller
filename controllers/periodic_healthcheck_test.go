package controllers

import (
	"context"
	"fmt"
	"math/rand"
	"testing"

	etcdv1 "github.com/aws/etcdadm-controller/api/v1beta1"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/collections"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// This test verifies that the periodicEtcdMembersHealthCheck does not panic when a Machine corresponding to an ETCD endpoint doesn not exist.
func TestReconcilePerodicHealthCheckEnsureNoPanic(t *testing.T) {
	cluster := newClusterWithExternalEtcd()
	etcdadmCluster := newEtcdadmCluster(cluster)
	ctx := context.Background()

	ownedMachine := newEtcdMachineWithEndpoint(etcdadmCluster, cluster)
	ownedMachines := make(collections.Machines, 1)
	ownedMachines.Insert(ownedMachine)

	etcdadmCluster.UID = "test-uid"
	etcdadmClusterMapper := make(map[types.UID]etcdadmClusterMemberHealthConfig, 1)

	ownedMachineEndpoint := ownedMachine.Status.Addresses[0].Address
	etcdadmCluster.Status.Endpoints = ownedMachineEndpoint

	// This translates to an ETCD endpoint that doesn't correspond to any machine.
	endpointToMachineMapper := make(map[string]*clusterv1.Machine)
	endpointToMachineMapper[ownedMachineEndpoint] = nil

	etcdadmClusterMapper[etcdadmCluster.UID] = etcdadmClusterMemberHealthConfig{
		unhealthyMembersFrequency: make(map[string]int),
		unhealthyMembersToRemove:  make(map[string]*clusterv1.Machine),
		endpointToMachineMapper:   endpointToMachineMapper,
		cluster:                   cluster,
		ownedMachines:             ownedMachines,
	}

	objects := []client.Object{
		cluster,
		etcdadmCluster,
		infraTemplate.DeepCopy(),
		ownedMachine,
	}
	fakeClient := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(objects...).Build()

	r := &EtcdadmClusterReconciler{
		Client:         fakeClient,
		uncachedClient: fakeClient,
		Log:            log.Log,
	}

	// This ensures that the test did not panic.
	defer func() {
		if rcv := recover(); rcv != nil {
			t.Errorf("code should not have panicked: %v", rcv)
		}
	}()

	_ = r.periodicEtcdMembersHealthCheck(ctx, cluster, etcdadmCluster, etcdadmClusterMapper)
}

// newEtcdMachineWithEndpoint returns a new machine with a random IP address.
func newEtcdMachineWithEndpoint(etcdadmCluster *etcdv1.EtcdadmCluster, cluster *clusterv1.Cluster) *clusterv1.Machine {
	machine := newEtcdMachine(etcdadmCluster, cluster)
	machine.Status.Addresses = []clusterv1.MachineAddress{
		{
			Type:    clusterv1.MachineExternalIP,
			Address: fmt.Sprintf("%d.%d.%d.%d", rand.Intn(256), rand.Intn(256), rand.Intn(256), rand.Intn(256)),
		},
	}
	return machine
}
