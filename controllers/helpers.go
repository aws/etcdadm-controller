package controllers

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"

	etcdbootstrapv1 "github.com/aws/etcdadm-bootstrap-provider/api/v1beta1"
	etcdv1 "github.com/mrajashree/etcdadm-controller/api/v1beta1"
	"github.com/pkg/errors"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apiserver/pkg/storage/names"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
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
		must(labels.NewRequirement(clusterv1.ClusterLabelName, selection.Equals, []string{clusterName})),
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
		clusterv1.ClusterLabelName:            clusterName,
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
	infraRef, err := external.CloneTemplate(ctx, &external.CloneTemplateInput{
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
	if infraRef == nil {
		return ctrl.Result{}, fmt.Errorf("infrastructure template could not be cloned for etcd machine")
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

func (r *EtcdadmClusterReconciler) generateEtcdadmConfig(ctx context.Context, ec *etcdv1.EtcdadmCluster, cluster *clusterv1.Cluster) (*corev1.ObjectReference, error) {
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
	bootstrapRef := &corev1.ObjectReference{
		APIVersion: etcdbootstrapv1.GroupVersion.String(),
		Kind:       "EtcdadmConfig",
		Name:       bootstrapConfig.GetName(),
		Namespace:  bootstrapConfig.GetNamespace(),
		UID:        bootstrapConfig.GetUID(),
	}

	if err := r.Client.Create(ctx, bootstrapConfig); err != nil {
		return nil, errors.Wrap(err, "Failed to create etcdadm bootstrap configuration")
	}

	return bootstrapRef, nil
}

func (r *EtcdadmClusterReconciler) generateMachine(ctx context.Context, ec *etcdv1.EtcdadmCluster, cluster *clusterv1.Cluster, infraRef, bootstrapRef *corev1.ObjectReference, failureDomain *string) error {
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
			InfrastructureRef: *infraRef,
			Bootstrap: clusterv1.Bootstrap{
				ConfigRef: bootstrapRef,
			},
			FailureDomain: failureDomain,
		},
	}
	if err := r.Client.Create(ctx, machine); err != nil {
		return errors.Wrap(err, "failed to create machine")
	}
	return nil
}

func getEtcdMachineAddress(machine *clusterv1.Machine) string {
	var foundAddress bool
	var machineAddress string
	for _, address := range machine.Status.Addresses {
		if address.Type == clusterv1.MachineInternalIP || address.Type == clusterv1.MachineInternalDNS {
			machineAddress = address.Address
			foundAddress = true
			break
		}
	}
	for _, address := range machine.Status.Addresses {
		if !foundAddress {
			if address.Type == clusterv1.MachineExternalIP || address.Type == clusterv1.MachineExternalDNS {
				machineAddress = address.Address
				break
			}
		}
	}
	return machineAddress
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

	obj, err := external.Get(ctx, r.Client, &ref, cluster.Namespace)
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
