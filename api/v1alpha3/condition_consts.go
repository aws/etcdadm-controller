package v1alpha3

import clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"

const (
	// EtcdMachinesSpecUpToDateCondition documents that the spec of the machines controlled by the EtcdadmCluster
	// is up to date. When this condition is false, the EtcdadmCluster is executing a rolling upgrade.
	EtcdMachinesSpecUpToDateCondition clusterv1.ConditionType = "EtcdMachinesSpecUpToDate"

	// EtcdRollingUpdateInProgressReason (Severity=Warning) documents an EtcdadmCluster object executing a
	// rolling upgrade for aligning the machines spec to the desired state.
	EtcdRollingUpdateInProgressReason = "EtcdRollingUpdateInProgress"

	// EtcdCertificatesAvailableCondition indicates that the etcdadm controller has generated the etcd certs to be used by new members
	// joining the etcd cluster, and to be used by the controlplane
	EtcdCertificatesAvailableCondition clusterv1.ConditionType = "EtcdCertificatesAvailable"
)
