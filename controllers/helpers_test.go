package controllers

import (
	"testing"

	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

func TestGetEtcdMachineAddress(t *testing.T) {
	g := NewWithT(t)
	type test struct {
		machine        clusterv1.Machine
		AvailAddrTypes clusterv1.MachineAddresses
		wantAddr       string
	}

	capiMachine := clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name: "machine",
		},
		Spec: clusterv1.MachineSpec{
			ClusterName: "test-cluster",
		},
		Status: clusterv1.MachineStatus{},
	}
	tests := []test{
		{
			machine: capiMachine,
			AvailAddrTypes: clusterv1.MachineAddresses{
				clusterv1.MachineAddress{
					Type:    clusterv1.MachineInternalIP,
					Address: "1.1.1.1",
				}, clusterv1.MachineAddress{
					Type:    clusterv1.MachineExternalIP,
					Address: "2.2.2.2",
				},
			},
			wantAddr: "2.2.2.2",
		}, {
			machine: capiMachine,
			AvailAddrTypes: []clusterv1.MachineAddress{
				{
					Type:    clusterv1.MachineInternalDNS,
					Address: "1.1.1.1",
				}, {
					Type:    clusterv1.MachineExternalIP,
					Address: "2.2.2.2",
				},
			},
			wantAddr: "2.2.2.2",
		}, {
			machine: capiMachine,
			AvailAddrTypes: []clusterv1.MachineAddress{
				{
					Type:    clusterv1.MachineInternalIP,
					Address: "1.1.1.1",
				}, {
					Type:    clusterv1.MachineInternalDNS,
					Address: "2.2.2.2",
				},
			},
			wantAddr: "2.2.2.2",
		}, {
			machine: capiMachine,
			AvailAddrTypes: []clusterv1.MachineAddress{
				{
					Type:    clusterv1.MachineExternalDNS,
					Address: "1.1.1.1",
				}, {
					Type:    clusterv1.MachineInternalDNS,
					Address: "2.2.2.2",
				},
			},
			wantAddr: "1.1.1.1",
		}, {
			machine: capiMachine,
			AvailAddrTypes: []clusterv1.MachineAddress{
				{
					Type:    clusterv1.MachineHostName,
					Address: "1.1.1.1",
				},
			},
			wantAddr: "",
		},
	}
	for _, tc := range tests {
		capiMachine.Status.Addresses = tc.AvailAddrTypes
		g.Expect(getEtcdMachineAddress(&capiMachine)).To(Equal(tc.wantAddr))
	}
}
