package controllers

import (
	"context"
	"fmt"

	etcdbpv1alpha4 "github.com/mrajashree/etcdadm-bootstrap-provider/api/v1alpha3"
	etcdv1 "github.com/mrajashree/etcdadm-controller/api/v1alpha3"
	"github.com/pkg/errors"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/storage/names"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"
	"sigs.k8s.io/cluster-api/controllers/external"
	"sigs.k8s.io/cluster-api/util/collections"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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
		Labels:      EtcdLabelsForCluster(cluster.Name),
	})

	r.Log.Info(fmt.Sprintf("Is infraRef nil?: %v", infraRef == nil))
	if infraRef == nil {
		return ctrl.Result{}, fmt.Errorf("infraRef is nil")
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
	bootstrapConfig := &etcdbpv1alpha4.EtcdadmConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:            names.SimpleNameGenerator.GenerateName(ec.Name + "-"),
			Namespace:       ec.Namespace,
			Labels:          EtcdLabelsForCluster(cluster.Name),
			OwnerReferences: []metav1.OwnerReference{owner},
		},
		Spec: ec.Spec.EtcdadmConfigSpec,
	}
	bootstrapRef := &corev1.ObjectReference{
		APIVersion: etcdbpv1alpha4.GroupVersion.String(),
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
			Labels:    EtcdLabelsForCluster(cluster.Name),
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

func (r *EtcdadmClusterReconciler) changeClusterInitAddress(ctx context.Context, ec *etcdv1.EtcdadmCluster, cluster *clusterv1.Cluster, ep *EtcdPlane, machineAddress string, machineToDelete *clusterv1.Machine) error {
	secretNameNs := client.ObjectKey{Name: ec.Status.InitMachineAddress, Namespace: cluster.Namespace}
	secretInitAddress := &corev1.Secret{}
	if err := r.Client.Get(ctx, secretNameNs, secretInitAddress); err != nil {
		return err
	}
	currentInitAddress := string(secretInitAddress.Data["address"])
	if currentInitAddress != machineAddress {
		// Machine being deleted is not the machine whose address is used by members joining, noop
		return nil
	}
	upToDateMachines := ep.UpToDateMachines()
	var newInitAddress string
	if len(upToDateMachines) == 0 {
		// This can happen during an upgrade if the first node picked for scale down is the init node
		// Get the address from any of the other machines
		r.Log.Info("First machine picked during upgrade scale down is init machine, so replacing with one of the existing machines")
		for _, m := range ep.Machines.Difference(collections.NewFilterableMachineCollection(machineToDelete)) {
			newInitAddress = getEtcdMachineAddress(m)
			r.Log.Info(fmt.Sprintf("Picking non updated machine: %v", newInitAddress))
			break
		}
	} else {
		for _, m := range upToDateMachines {
			newInitAddress = getEtcdMachineAddress(m)
			r.Log.Info(fmt.Sprintf("Picking fully updated machine: %v", newInitAddress))
			break
		}
	}
	if newInitAddress == "" {
		return fmt.Errorf("Could not find a machine to use to join etcd cluster as a member")
	}

	secretInitAddress.Data["address"] = []byte(newInitAddress)
	return r.Client.Update(ctx, secretInitAddress)
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
