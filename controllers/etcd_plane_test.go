package controllers

import (
	"testing"

	etcdbootstrapv1 "github.com/aws/etcdadm-bootstrap-provider/api/v1beta1"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestOutOfDateMachines(t *testing.T) {
	g := NewWithT(t)

	cluster := newClusterWithExternalEtcd()
	etcdadmCluster := newEtcdadmCluster(cluster)

	machine1 := newEtcdMachine(etcdadmCluster, cluster)

	objects := []client.Object{
		cluster,
		etcdadmCluster,
		infraTemplate.DeepCopy(),
		machine1,
	}

	fakeClient := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(objects...).Build()

	machines := map[string]*clusterv1.Machine{
		machine1.Name: machine1,
	}

	etcdadmConfigs, err := getEtcdadmConfigs(ctx, fakeClient, machines)
	g.Expect(err).ToNot(HaveOccurred())
	infraResources, err := getInfraResources(ctx, fakeClient, machines)
	g.Expect(err).ToNot(HaveOccurred())

	// build EtcdPlane for test
	ep := &EtcdPlane{
		EC:             etcdadmCluster,
		Cluster:        cluster,
		Machines:       machines,
		etcdadmConfigs: etcdadmConfigs,
		infraResources: infraResources,
	}

	outdatedMachines := ep.OutOfDateMachines()
	g.Expect(len(outdatedMachines)).To(Equal(0))

	// change etcdadmConfig for machine
	ep.etcdadmConfigs[machine1.Name] = &etcdbootstrapv1.EtcdadmConfig{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      testClusterName,
		},
		Spec: etcdbootstrapv1.EtcdadmConfigSpec{
			EtcdadmInstallCommands: []string{"etcdadmInstallCommands is not empty"},
			CloudInitConfig: &etcdbootstrapv1.CloudInitConfig{
				Version: "v3.4.9",
			},
		},
	}

	// check that machine is in outdated machines
	outdatedMachines = ep.OutOfDateMachines()
	g.Expect(len(outdatedMachines)).To(Equal(1))
}
