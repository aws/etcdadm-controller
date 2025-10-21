package controllers

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"

	etcdbootstrapv1 "github.com/aws/etcdadm-bootstrap-provider/api/v1beta1"
	etcdv1 "github.com/aws/etcdadm-controller/api/v1beta1"
	"github.com/pkg/errors"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apiserver/pkg/storage/names"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/controllers/external"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	httpsPrefix       = "https://"
	etcdClientURLPort = "2379"
)

// EtcdMachinesSelectorForCluster returns the label selector necessary to get etcd machines for a given cluster.
func EtcdMachinesSelectorForCluster(clusterName, etcdClusterName string) labels.Selector {
	must := func(r *labels.Requirement, err error) labels.Requirement {
		if err != nil {
			panic(err)
		}
		return *r
	}
	return labels.NewSelector().Add(
		must(labels.NewRequirement(clusterv1.ClusterNameLabel, selection.Equals, []string{clusterName})),
		must(labels.NewRequirement(clusterv1.MachineEtcdClusterLabelName, selection.Equals, []string{etcdClusterName})),
	)
}

// EtcdClusterMachines returns a filter to find all etcd machines for a cluster, regardless of ownership.
func EtcdClusterMachines(clusterName, etcdClusterName string) func(machine *clusterv1.Machine) bool {
	selector := EtcdMachinesSelectorForCluster(clusterName, etcdClusterName)
	return func(machine *clusterv1.Machine) bool {
		if machine == nil {
			return false
		}
		return selector.Matches(labels.Set(machine.Labels))
	}
}

// ControlPlaneLabelsForCluster returns a set of labels to add to a control plane machine for this specific cluster.
func EtcdLabelsForCluster(clusterName string, etcdClusterName string) map[string]string {
	return map[string]string{
		clusterv1.ClusterNameLabel:            clusterName,
		clusterv1.MachineEtcdClusterLabelName: etcdClusterName,
	}
}

func (r *EtcdadmClusterReconciler) cloneConfigsAndGenerateMachine(ctx context.Context, ec *etcdv1.EtcdadmCluster, cluster *clusterv1.Cluster, failureDomain *string) (ctrl.Result, error) {
	// Since the cloned resource should eventually have a controller ref for the Machine, we create an
	// OwnerReference here without the Controller field set
	infraCloneOwner := &metav1.OwnerReference{
		APIVersion: etcdv1.GroupVersion.String(),
		Kind:       "EtcdadmCluster",
		Name:       ec.Name,
		UID:        ec.UID,
	}

	// Clone the infrastructure template
	infraObj, _, err := external.CreateFromTemplate(ctx, &external.CreateFromTemplateInput{
		Client:      r.Client,
		TemplateRef: &ec.Spec.InfrastructureTemplate,
		Namespace:   ec.Namespace,
		OwnerRef:    infraCloneOwner,
		ClusterName: cluster.Name,
		Labels:      EtcdLabelsForCluster(cluster.Name, ec.Name),
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error cloning infrastructure template for etcd machine: %v", err)
	}
	if infraObj == nil {
		return ctrl.Result{}, fmt.Errorf("infrastructure template could not be cloned for etcd machine")
	}

	// Convert unstructured object to ContractVersionedObjectReference
	gv, err := schema.ParseGroupVersion(infraObj.GetAPIVersion())
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to parse infrastructure object API version: %v", err)
	}
	infraRef := clusterv1.ContractVersionedObjectReference{
		Kind:     infraObj.GetKind(),
		Name:     infraObj.GetName(),
		APIGroup: gv.Group,
	}

	bootstrapRef, err := r.generateEtcdadmConfig(ctx, ec, cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.generateMachine(ctx, ec, cluster, infraRef, bootstrapRef, failureDomain); err != nil {
		r.Log.Error(err, "Failed to create initial etcd machine")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *EtcdadmClusterReconciler) generateEtcdadmConfig(ctx context.Context, ec *etcdv1.EtcdadmCluster, cluster *clusterv1.Cluster) (clusterv1.ContractVersionedObjectReference, error) {
	owner := metav1.OwnerReference{
		APIVersion: etcdv1.GroupVersion.String(),
		Kind:       "EtcdadmCluster",
		Name:       ec.Name,
		UID:        ec.UID,
	}
	bootstrapConfig := &etcdbootstrapv1.EtcdadmConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:            names.SimpleNameGenerator.GenerateName(ec.Name + "-"),
			Namespace:       ec.Namespace,
			Labels:          EtcdLabelsForCluster(cluster.Name, ec.Name),
			OwnerReferences: []metav1.OwnerReference{owner},
		},
		Spec: ec.Spec.EtcdadmConfigSpec,
	}

	if err := r.Client.Create(ctx, bootstrapConfig); err != nil {
		return clusterv1.ContractVersionedObjectReference{}, errors.Wrap(err, "Failed to create etcdadm bootstrap configuration")
	}

	bootstrapRef := clusterv1.ContractVersionedObjectReference{
		Kind:     "EtcdadmConfig",
		Name:     bootstrapConfig.GetName(),
		APIGroup: etcdbootstrapv1.GroupVersion.Group,
	}

	return bootstrapRef, nil
}

func (r *EtcdadmClusterReconciler) generateMachine(ctx context.Context, ec *etcdv1.EtcdadmCluster, cluster *clusterv1.Cluster, infraRef clusterv1.ContractVersionedObjectReference, bootstrapRef clusterv1.ContractVersionedObjectReference, failureDomain *string) error {
	var failureDomainStr string
	if failureDomain != nil {
		failureDomainStr = *failureDomain
	}

	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.SimpleNameGenerator.GenerateName(ec.Name + "-"),
			Namespace: ec.Namespace,
			Labels:    EtcdLabelsForCluster(cluster.Name, ec.Name),
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(ec, etcdv1.GroupVersion.WithKind("EtcdadmCluster")),
			},
		},
		Spec: clusterv1.MachineSpec{
			ClusterName:       cluster.Name,
			InfrastructureRef: infraRef,
			Bootstrap: clusterv1.Bootstrap{
				ConfigRef: bootstrapRef,
			},
			FailureDomain: failureDomainStr,
		},
	}
	if err := r.Client.Create(ctx, machine); err != nil {
		return errors.Wrap(err, "failed to create machine")
	}
	return nil
}

func getEtcdMachineAddress(machine *clusterv1.Machine) string {
	var internalIP, internalDNS, externalIP, externalDNS string

	// Check and record all different address types set for the machine and return later according to precedence.
	for _, address := range machine.Status.Addresses {
		switch address.Type {
		case clusterv1.MachineInternalIP:
			internalIP = address.Address
		case clusterv1.MachineInternalDNS:
			internalDNS = address.Address
		case clusterv1.MachineExternalIP:
			externalIP = address.Address
		case clusterv1.MachineExternalDNS:
			externalDNS = address.Address
		}
	}

	// The order of these checks determines the precedence of the address to use
	if externalDNS != "" {
		return externalDNS
	} else if externalIP != "" {
		return externalIP
	} else if internalDNS != "" {
		return internalDNS
	} else if internalIP != "" {
		return internalIP
	}

	return ""
}

func getMemberClientURL(address string) string {
	return fmt.Sprintf("%s%s:%s", httpsPrefix, address, etcdClientURLPort)
}

func getEtcdMachineAddressFromClientURL(clientURL string) string {
	u, err := url.ParseRequestURI(clientURL)
	if err != nil {
		return ""
	}
	host, _, err := net.SplitHostPort(u.Host)
	if err != nil {
		return ""
	}
	return host
}

func getMemberHealthCheckEndpoint(clientURL string) string {
	return fmt.Sprintf("%s/health", clientURL)
}

// source: https://github.com/kubernetes-sigs/etcdadm/blob/master/etcd/etcd.go#L53:6
func memberForPeerURLs(members *clientv3.MemberListResponse, peerURLs []string) (*etcdserverpb.Member, bool) {
	for _, m := range members.Members {
		if stringSlicesEqual(m.PeerURLs, peerURLs) {
			return m, true
		}
	}
	return nil, false
}

// stringSlicesEqual compares two string slices for equality
func stringSlicesEqual(l, r []string) bool {
	if len(l) != len(r) {
		return false
	}
	for i := range l {
		if l[i] != r[i] {
			return false
		}
	}
	return true
}

// Logic & implementation similar to KCP controller reconciling external MachineTemplate InfrastrucutureReference https://github.com/kubernetes-sigs/cluster-api/blob/master/controlplane/kubeadm/controllers/helpers.go#L123:41
func (r *EtcdadmClusterReconciler) reconcileExternalReference(ctx context.Context, cluster *clusterv1.Cluster, ref corev1.ObjectReference) error {
	if !strings.HasSuffix(ref.Kind, clusterv1.TemplateSuffix) {
		return nil
	}

	obj, err := external.Get(ctx, r.Client, &ref)
	if err != nil {
		return err
	}

	// Note: We intentionally do not handle checking for the paused label on an external template reference

	patchHelper, err := patch.NewHelper(obj, r.Client)
	if err != nil {
		return err
	}

	obj.SetOwnerReferences(util.EnsureOwnerRef(obj.GetOwnerReferences(), metav1.OwnerReference{
		APIVersion: clusterv1.GroupVersion.String(),
		Kind:       "Cluster",
		Name:       cluster.Name,
		UID:        cluster.UID,
	}))

	return patchHelper.Patch(ctx, obj)
}
