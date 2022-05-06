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

	// EtcdClusterResizeCompleted indicates if cluster is finished with scale up/down or is being resized
	EtcdClusterResizeCompleted clusterv1.ConditionType = "EtcdClusterResizeCompleted"

	// EtcdScaleUpInProgressReason indicates scale up is in progress
	EtcdScaleUpInProgressReason = "ScalingUp"

	// EtcdScaleDownInProgressReason indicates scale down is in progress
	EtcdScaleDownInProgressReason = "ScalingDown"

	// InitializedCondition shows if etcd cluster has been initialized, which is when the first etcd member has been initialized
	InitializedCondition clusterv1.ConditionType = "Initialized"

	// WaitingForEtcdadmInitReason shows that the first etcd member has not been created yet
	WaitingForEtcdadmInitReason = "WaitingForEtcdadmInit"

	// EtcdMachinesReadyCondition stores an aggregate status of all owned machines
	EtcdMachinesReadyCondition clusterv1.ConditionType = "EtcdMachinesReady"

	// EtcdClusterHasNoOutdatedMembersCondition indicates that all etcd members are up-to-date. NOTE: this includes even members present on Machines not owned by the
	// etcdadm cluster
	EtcdClusterHasNoOutdatedMembersCondition clusterv1.ConditionType = "EtcdClusterHasNoOutdatedMachines"

	// EtcdClusterHasOutdatedMembersReason shows that some of the etcd members are out-of-date
	EtcdClusterHasOutdatedMembersReason = "EtcdClusterHasOutdatedMachines"
)
