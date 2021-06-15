package controllers

import clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"

// ControlPlaneLabelsForCluster returns a set of labels to add to a control plane machine for this specific cluster.
func EtcdLabelsForCluster(clusterName string) map[string]string {
	return map[string]string{
		clusterv1.ClusterLabelName:   clusterName,
		clusterv1.MachineEtcdClusterLabelName: "",
	}
}
