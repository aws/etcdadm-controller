package controllers

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"time"

	etcdbpv1alpha4 "github.com/mrajashree/etcdadm-bootstrap-provider/api/v1alpha4"
	etcdv1 "github.com/mrajashree/etcdadm-controller/api/v1alpha4"
	"github.com/pkg/errors"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/storage/names"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha4"
	"sigs.k8s.io/cluster-api/util/collections"
	"sigs.k8s.io/cluster-api/util/failuredomains"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/etcdadm/constants"
)

type EtcdPlane struct {
	EC                   *etcdv1.EtcdCluster
	Cluster              *clusterv1.Cluster
	Machines             collections.Machines
	machinesPatchHelpers map[string]*patch.Helper

	//etcdadmConfigs map[string]*etcdbpv1alpha4.EtcdadmConfig
	//infraResources map[string]*unstructured.Unstructured
}

func NewEtcdPlane(ctx context.Context, client client.Client, cluster *clusterv1.Cluster, ec *etcdv1.EtcdCluster, ownedMachines collections.Machines) (*EtcdPlane, error) {
	patchHelpers := map[string]*patch.Helper{}
	for _, machine := range ownedMachines {
		patchHelper, err := patch.NewHelper(machine, client)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create patch helper for machine %s", machine.Name)
		}
		patchHelpers[machine.Name] = patchHelper
	}

	return &EtcdPlane{
		EC:                   ec,
		Cluster:              cluster,
		Machines:             ownedMachines,
		machinesPatchHelpers: patchHelpers,
	}, nil
}

func (r *EtcdClusterReconciler) intializeEtcdCluster(ctx context.Context, ec *etcdv1.EtcdCluster, cluster *clusterv1.Cluster, ep *EtcdPlane) (ctrl.Result, error) {
	if err := r.generateCAandClientCertSecrets(ctx, cluster, ec); err != nil {
		r.Log.Error(err, "error generating etcd CA certs")
		return ctrl.Result{}, err
	}
	fd := ep.NextFailureDomainForScaleUp()
	return r.cloneConfigsAndGenerateMachine(ctx, ec, cluster, fd)
}

func (r *EtcdClusterReconciler) scaleUpEtcdCluster(ctx context.Context, ec *etcdv1.EtcdCluster, cluster *clusterv1.Cluster, ep *EtcdPlane) (ctrl.Result, error) {
	fd := ep.NextFailureDomainForScaleUp()
	return r.cloneConfigsAndGenerateMachine(ctx, ec, cluster, fd)
}

func (r *EtcdClusterReconciler) scaleDownEtcdCluster(ctx context.Context, ec *etcdv1.EtcdCluster, cluster *clusterv1.Cluster, ep *EtcdPlane, outdatedMachines collections.Machines) (ctrl.Result, error) {
	// Pick the Machine that we should scale down.
	machineToDelete, err := selectMachineForScaleDown(ep, outdatedMachines)
	if err != nil || machineToDelete == nil {
		return ctrl.Result{}, errors.Wrap(err, "failed to select machine for scale down")
	}

	//var localMember *etcdserverpb.Member
	log := r.Log
	caCertPool := x509.NewCertPool()
	caCert, err := r.getCACert(ctx, cluster)
	if err != nil {
		return ctrl.Result{}, err
	}
	caCertPool.AppendCertsFromPEM(caCert)

	clientCert, err := r.getClientCerts(ctx, cluster)
	if err != nil {
		return ctrl.Result{}, errors.Wrap(err, "Error getting client cert for healthcheck")
	}

	var foundAddress bool
	var endpoint, peerURL string
	// TODO: save endpoint on the EtcdadmConfig status object
	for _, address := range machineToDelete.Status.Addresses {
		if address.Type == clusterv1.MachineInternalIP || address.Type == clusterv1.MachineInternalDNS {
			endpoint = fmt.Sprintf("https://%s:2379", address.Address)
			peerURL = fmt.Sprintf("https://%s:2380", address.Address)
			foundAddress = true
			break
		}
	}
	for _, address := range machineToDelete.Status.Addresses {
		if !foundAddress {
			if address.Type == clusterv1.MachineExternalIP || address.Type == clusterv1.MachineExternalDNS {
				endpoint += fmt.Sprintf("https://%s:2379", address.Address)
				peerURL = fmt.Sprintf("https://%s:2380", address.Address)
				break
			}
		}
	}
	etcdClient, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{endpoint},
		DialTimeout: 5 * time.Second,
		TLS: &tls.Config{
			RootCAs:      caCertPool,
			Certificates: []tls.Certificate{clientCert},
		},
	})
	if etcdClient == nil || err != nil {
		log.Info("cloud not create etcd client")
		return ctrl.Result{}, err
	}
	etcdCtx, cancel := context.WithTimeout(ctx, constants.DefaultEtcdRequestTimeout)
	mresp, err := etcdClient.MemberList(etcdCtx)
	cancel()
	if err != nil {
		log.Error(err, "Error listing members: %v")
		return ctrl.Result{}, err
	}

	localMember, ok := memberForPeerURLs(mresp, []string{peerURL})
	if ok {
		log.Info("[membership] Member was not removed")
		if len(mresp.Members) > 1 {
			log.Info("[membership] Removing member")
			etcdCtx, cancel = context.WithTimeout(ctx, constants.DefaultEtcdRequestTimeout)
			_, err = etcdClient.MemberRemove(etcdCtx, localMember.ID)
			cancel()
			if err != nil {
				log.Error(err, "[membership] Error removing member: %v")
			}
			if err := r.Client.Delete(ctx, machineToDelete); err != nil && !apierrors.IsNotFound(err) {
				log.Error(err, "Failed to delete etcd machine")
				return ctrl.Result{}, err
			}
		} else {
			log.Info("[membership] Not removing member because it is the last in the cluster")
		}
	} else {
		log.Info("[membership] Member was removed")
	}

	return ctrl.Result{}, nil
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

func selectMachineForScaleDown(ep *EtcdPlane, outdatedMachines collections.Machines) (*clusterv1.Machine, error) {
	machines := ep.Machines
	switch {
	case ep.MachineWithDeleteAnnotation(outdatedMachines).Len() > 0:
		machines = ep.MachineWithDeleteAnnotation(outdatedMachines)
	case ep.MachineWithDeleteAnnotation(machines).Len() > 0:
		machines = ep.MachineWithDeleteAnnotation(machines)
	case outdatedMachines.Len() > 0:
		machines = outdatedMachines
	}
	return ep.MachineInFailureDomainWithMostMachines(machines)
}

func (r *EtcdClusterReconciler) generateEtcdadmConfig(ctx context.Context, ec *etcdv1.EtcdCluster, cluster *clusterv1.Cluster) (*corev1.ObjectReference, error) {
	owner := metav1.OwnerReference{
		APIVersion: etcdv1.GroupVersion.String(),
		Kind:       "EtcdCluster",
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

func (r *EtcdClusterReconciler) generateMachine(ctx context.Context, ec *etcdv1.EtcdCluster, cluster *clusterv1.Cluster, infraRef, bootstrapRef *corev1.ObjectReference, failureDomain *string) error {
	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.SimpleNameGenerator.GenerateName(ec.Name + "-"),
			Namespace: ec.Namespace,
			Labels:    EtcdLabelsForCluster(cluster.Name),
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(ec, etcdv1.GroupVersion.WithKind("EtcdCluster")),
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

// MachineWithDeleteAnnotation returns a machine that has been annotated with DeleteMachineAnnotation key.
func (ep *EtcdPlane) MachineWithDeleteAnnotation(machines collections.Machines) collections.Machines {
	// See if there are any machines with DeleteMachineAnnotation key.
	annotatedMachines := machines.Filter(collections.HasAnnotationKey(clusterv1.DeleteMachineAnnotation))
	// If there are, return list of annotated machines.
	return annotatedMachines
}

// FailureDomainWithMostMachines returns a fd which has the most machines on it.
func (ep *EtcdPlane) FailureDomainWithMostMachines(machines collections.Machines) *string {
	// See if there are any Machines that are not in currently defined failure domains first.
	notInFailureDomains := machines.Filter(
		collections.Not(collections.InFailureDomains(ep.FailureDomains().GetIDs()...)),
	)
	if len(notInFailureDomains) > 0 {
		// return the failure domain for the oldest Machine not in the current list of failure domains
		// this could be either nil (no failure domain defined) or a failure domain that is no longer defined
		// in the cluster status.
		return notInFailureDomains.Oldest().Spec.FailureDomain
	}
	return failuredomains.PickMost(ep.Cluster.Status.FailureDomains, ep.Machines, machines)
}

// MachineInFailureDomainWithMostMachines returns the first matching failure domain with machines that has the most control-plane machines on it.
func (ep *EtcdPlane) MachineInFailureDomainWithMostMachines(machines collections.Machines) (*clusterv1.Machine, error) {
	fd := ep.FailureDomainWithMostMachines(machines)
	machinesInFailureDomain := machines.Filter(collections.InFailureDomains(fd))
	machineToMark := machinesInFailureDomain.Oldest()
	if machineToMark == nil {
		return nil, errors.New("failed to pick control plane Machine to mark for deletion")
	}
	return machineToMark, nil
}

// NextFailureDomainForScaleUp returns the failure domain with the fewest number of up-to-date machines.
func (ep *EtcdPlane) NextFailureDomainForScaleUp() *string {
	if len(ep.Cluster.Status.FailureDomains) == 0 {
		return nil
	}
	return failuredomains.PickFewest(ep.FailureDomains(), ep.UpToDateMachines())
}

// FailureDomains returns a slice of failure domain objects synced from the infrastructure provider into Cluster.Status.
func (ep *EtcdPlane) FailureDomains() clusterv1.FailureDomains {
	if ep.Cluster.Status.FailureDomains == nil {
		return clusterv1.FailureDomains{}
	}
	return ep.Cluster.Status.FailureDomains
}

// UpToDateMachines returns the machines that are up to date with the control
// plane's configuration and therefore do not require rollout.
func (ep *EtcdPlane) UpToDateMachines() collections.Machines {
	return ep.Machines.Difference(ep.MachinesNeedingRollout())
}

// MachinesNeedingRollout return a list of machines that need to be rolled out.
func (ep *EtcdPlane) MachinesNeedingRollout() collections.Machines {
	// Ignore machines to be deleted.
	machines := ep.Machines.Filter(collections.Not(collections.HasDeletionTimestamp))

	// Return machines if they are scheduled for rollout or if with an outdated configuration.
	return machines.AnyFilter(
	// Machines that are scheduled for rollout (KCP.Spec.UpgradeAfter set, the UpgradeAfter deadline is expired, and the machine was created before the deadline).
	//collections.ShouldRolloutAfter(&ep.reconciliationTime, ep.KCP.Spec.UpgradeAfter),
	// Machines that do not match with KCP config.
	//collections.Not(MatchesKCPConfiguration(c.infraResources, c.kubeadmConfigs, c.KCP)),
	)
}
