package controllers

import (
	"testing"

	etcdv1 "github.com/aws/etcdadm-controller/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"

	. "github.com/onsi/gomega"
)

func TestUpdateStatusResizeIncomplete(t *testing.T) {
	g := NewWithT(t)

	cluster := newClusterWithExternalEtcd()
	etcdadmCluster := newEtcdadmCluster(cluster)

	machine1 := newEtcdMachine(etcdadmCluster, cluster)
	machine2 := newEtcdMachine(etcdadmCluster, cluster)

	etcdMachine1 := etcdMachine{
		Machine:     machine1,
		endpoint:    "1.1.1.1",
		listening:   true,
		healthError: nil,
	}
	etcdMachine2 := etcdMachine{
		Machine:     machine2,
		endpoint:    "1.1.1.1",
		listening:   true,
		healthError: nil,
	}

	ownedMachines := map[string]etcdMachine{
		"machine1": etcdMachine1,
		"machine2": etcdMachine2,
	}

	objects := []client.Object{
		cluster,
		etcdadmCluster,
		infraTemplate.DeepCopy(),
		machine1,
		machine2,
	}

	fakeClient := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(objects...).Build()

	r := &EtcdadmClusterReconciler{
		Client:         fakeClient,
		uncachedClient: fakeClient,
		Log:            log.Log,
	}

	err := r.updateStatus(ctx, etcdadmCluster, cluster, ownedMachines)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(conditions.IsTrue(etcdadmCluster, etcdv1.EtcdClusterResizeCompleted)).To(BeFalse())
}

func TestUpdateStatusResizeComplete(t *testing.T) {
	g := NewWithT(t)

	cluster := newClusterWithExternalEtcd()
	etcdadmCluster := newEtcdadmCluster(cluster)

	machine1 := newEtcdMachine(etcdadmCluster, cluster)
	machine2 := newEtcdMachine(etcdadmCluster, cluster)
	machine3 := newEtcdMachine(etcdadmCluster, cluster)

	etcdMachine1 := etcdMachine{
		Machine:     machine1,
		endpoint:    "1.1.1.1",
		listening:   true,
		healthError: nil,
	}
	etcdMachine2 := etcdMachine{
		Machine:     machine2,
		endpoint:    "1.1.1.1",
		listening:   true,
		healthError: nil,
	}
	etcdMachine3 := etcdMachine{
		Machine:     machine3,
		endpoint:    "1.1.1.1",
		listening:   true,
		healthError: nil,
	}

	ownedMachines := map[string]etcdMachine{
		"machine1": etcdMachine1,
		"machine2": etcdMachine2,
		"machine3": etcdMachine3,
	}

	objects := []client.Object{
		cluster,
		etcdadmCluster,
		infraTemplate.DeepCopy(),
		machine1,
		machine2,
		machine3,
	}

	fakeClient := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(objects...).Build()

	r := &EtcdadmClusterReconciler{
		Client:         fakeClient,
		uncachedClient: fakeClient,
		Log:            log.Log,
	}

	err := r.updateStatus(ctx, etcdadmCluster, cluster, ownedMachines)
	// Init secret not defined, so error will occur. This test checks that the resizeComplete condition is properly set
	// which happens before the updating init secret stage of updateStatus.
	g.Expect(err).To(HaveOccurred())
	g.Expect(conditions.IsTrue(etcdadmCluster, etcdv1.EtcdClusterResizeCompleted)).To(BeTrue())
}
