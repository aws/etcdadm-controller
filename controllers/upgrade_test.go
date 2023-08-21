package controllers

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	"k8s.io/utils/pointer"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/collections"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func TestEtcdadmClusterReconciler_upgradeEtcdClusterM_MachineIsRemovedFromOwnedMachines(t *testing.T) {
	cluster := newClusterWithExternalEtcd()
	baseEtcdadCluster := newEtcdadmCluster(cluster)

	testCases := []struct {
		name            string
		ownedMachines   []*clusterv1.Machine
		desiredReplicas int32
	}{
		{
			name:            "owned machines same as replicas",
			desiredReplicas: 3,
			ownedMachines: []*clusterv1.Machine{
				newEtcdMachine(baseEtcdadCluster, cluster),
				newEtcdMachine(baseEtcdadCluster, cluster),
				newEtcdMachine(baseEtcdadCluster, cluster),
			},
		},
		{
			name:            "more owned machines than replicas",
			desiredReplicas: 3,
			ownedMachines: []*clusterv1.Machine{
				newEtcdMachine(baseEtcdadCluster, cluster),
				newEtcdMachine(baseEtcdadCluster, cluster),
				newEtcdMachine(baseEtcdadCluster, cluster),
				newEtcdMachine(baseEtcdadCluster, cluster),
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := context.Background()

			objs := []client.Object{}
			for _, m := range tc.ownedMachines {
				objs = append(objs, m)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(setupScheme()).
				WithObjects(
					objs...,
				).Build()

			r := &EtcdadmClusterReconciler{
				Client:         fakeClient,
				uncachedClient: fakeClient,
				Log:            log.Log,
			}

			etcdCluster := baseEtcdadCluster.DeepCopy()
			etcdCluster.Spec.Replicas = pointer.Int32(tc.desiredReplicas)
			etcdPlane := &EtcdPlane{
				Cluster:  cluster,
				EC:       etcdCluster,
				Machines: collections.FromMachines(tc.ownedMachines...),
			}
			machines := collections.FromMachines(tc.ownedMachines[0])

			g.Expect(
				r.upgradeEtcdCluster(ctx, cluster, etcdCluster, etcdPlane, machines),
			).To(Equal(ctrl.Result{}))
		})
	}
}
