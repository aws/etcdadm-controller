package controllers

import (
	"context"
	"reflect"

	etcdbootstrapv1 "github.com/aws/etcdadm-bootstrap-provider/api/v1beta1"
	etcdv1 "github.com/aws/etcdadm-controller/api/v1beta1"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/controllers/external"
	"sigs.k8s.io/cluster-api/util/collections"
	"sigs.k8s.io/cluster-api/util/failuredomains"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type EtcdPlane struct {
	EC                   *etcdv1.EtcdadmCluster
	Cluster              *clusterv1.Cluster
	Machines             collections.Machines
	machinesPatchHelpers map[string]*patch.Helper
	etcdadmConfigs       map[string]*etcdbootstrapv1.EtcdadmConfig
	infraResources       map[string]*unstructured.Unstructured
}

func NewEtcdPlane(ctx context.Context, client client.Client, cluster *clusterv1.Cluster, ec *etcdv1.EtcdadmCluster, ownedMachines collections.Machines) (*EtcdPlane, error) {
	infraObjects, err := getInfraResources(ctx, client, ownedMachines)
	if err != nil {
		return nil, err
	}
	etcdadmConfigs, err := getEtcdadmConfigs(ctx, client, ownedMachines)
	if err != nil {
		return nil, err
	}
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
		infraResources:       infraObjects,
		etcdadmConfigs:       etcdadmConfigs,
	}, nil
}

// Etcdadm controller follows the same logic for selecting a machine to scale down as the KCP controller. Source: https://github.com/kubernetes-sigs/cluster-api/blob/master/controlplane/kubeadm/controllers/scale.go#L234
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

// MachineWithDeleteAnnotation returns a machine that has been annotated with DeleteMachineAnnotation key.
func (ep *EtcdPlane) MachineWithDeleteAnnotation(machines collections.Machines) collections.Machines {
	// See if there are any machines with DeleteMachineAnnotation key.
	annotatedMachines := machines.Filter(collections.HasAnnotationKey(clusterv1.DeleteMachineAnnotation))
	// If there are, return list of annotated machines.
	return annotatedMachines
}

// All functions related to failureDomains follow the same logic as KCP's failureDomain implementation, to leverage existing methods
// FailureDomainWithMostMachines returns a fd which has the most machines on it.
func (ep *EtcdPlane) FailureDomainWithMostMachines(machines collections.Machines) *string {
	// Get failure domain IDs
	failureDomainIDs := make([]string, 0, len(ep.FailureDomains()))
	for _, fd := range ep.FailureDomains() {
		failureDomainIDs = append(failureDomainIDs, fd.Name)
	}

	// See if there are any Machines that are not in currently defined failure domains first.
	notInFailureDomains := machines.Filter(
		collections.Not(collections.InFailureDomains(failureDomainIDs...)),
	)
	if len(notInFailureDomains) > 0 {
		// return the failure domain for the oldest Machine not in the current list of failure domains
		// this could be either nil (no failure domain defined) or a failure domain that is no longer defined
		// in the cluster status.
		return &notInFailureDomains.Oldest().Spec.FailureDomain
	}
	result := failuredomains.PickMost(context.TODO(), ep.Cluster.Status.FailureDomains, ep.Machines, machines)
	return &result
}

// MachineInFailureDomainWithMostMachines returns the first matching failure domain with machines that has the most control-plane machines on it.
func (ep *EtcdPlane) MachineInFailureDomainWithMostMachines(machines collections.Machines) (*clusterv1.Machine, error) {
	fd := ep.FailureDomainWithMostMachines(machines)
	var fdStr string
	if fd != nil {
		fdStr = *fd
	}
	machinesInFailureDomain := machines.Filter(collections.InFailureDomains(fdStr))
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
	result := failuredomains.PickFewest(context.TODO(), ep.FailureDomains(), ep.UpToDateMachines(), collections.Machines{})
	return &result
}

// FailureDomains returns a slice of failure domain objects synced from the infrastructure provider into Cluster.Status.
func (ep *EtcdPlane) FailureDomains() []clusterv1.FailureDomain {
	if ep.Cluster.Status.FailureDomains == nil {
		return []clusterv1.FailureDomain{}
	}
	return ep.Cluster.Status.FailureDomains
}

// UpToDateMachines returns the machines that are up to date with the control
// plane's configuration and therefore do not require rollout.
func (ep *EtcdPlane) UpToDateMachines() collections.Machines {
	return ep.Machines.Difference(ep.MachinesNeedingRollout())
}

func (ep *EtcdPlane) NewestUpToDateMachine() *clusterv1.Machine {
	upToDateMachines := ep.UpToDateMachines()
	return upToDateMachines.Newest()
}

// MachinesNeedingRollout return a list of machines that need to be rolled out.
func (ep *EtcdPlane) MachinesNeedingRollout() collections.Machines {
	// Ignore machines to be deleted.
	machines := ep.Machines.Filter(collections.Not(collections.HasDeletionTimestamp))

	// Return machines if they are scheduled for rollout or if with an outdated configuration.
	return machines.AnyFilter(
		//Machines that do not match with Etcdadm config.
		collections.Not(MatchesEtcdadmClusterConfiguration(ep.infraResources, ep.etcdadmConfigs, ep.EC)),
	)
}

// OutOfDateMachines return a list of all machines with an out of date config.
func (ep *EtcdPlane) OutOfDateMachines() collections.Machines {
	// Return machines if they are scheduled for rollout or if with an outdated configuration.
	return ep.Machines.AnyFilter(
		//Machines that do not match with Etcdadm config.
		collections.Not(MatchesEtcdadmClusterConfiguration(ep.infraResources, ep.etcdadmConfigs, ep.EC)),
	)
}

// MatchesEtcdadmClusterConfiguration returns a filter to find all machines that matches with EtcdadmCluster config and do not require any rollout.
// Etcd version and extra params, and infrastructure template need to be equivalent.
func MatchesEtcdadmClusterConfiguration(infraConfigs map[string]*unstructured.Unstructured, machineConfigs map[string]*etcdbootstrapv1.EtcdadmConfig, ec *etcdv1.EtcdadmCluster) func(machine *clusterv1.Machine) bool {
	return collections.And(
		MatchesEtcdadmConfig(machineConfigs, ec),
		MatchesTemplateClonedFrom(infraConfigs, ec),
	)
}

// MatchesEtcdadmConfig checks if machine's EtcdadmConfigSpec is equivalent with EtcdadmCluster's spec
func MatchesEtcdadmConfig(machineConfigs map[string]*etcdbootstrapv1.EtcdadmConfig, ec *etcdv1.EtcdadmCluster) collections.Func {
	return func(machine *clusterv1.Machine) bool {
		if machine == nil {
			return false
		}
		etcdadmConfig, found := machineConfigs[machine.Name]
		if !found {
			// Return true here because failing to get EtcdadmConfig should not be considered as unmatching.
			// This is a safety precaution to avoid rolling out machines if the client or the api-server is misbehaving.
			return true
		}

		ecConfig := ec.Spec.EtcdadmConfigSpec.DeepCopy()
		return reflect.DeepEqual(&etcdadmConfig.Spec, ecConfig)
	}
}

// MatchesTemplateClonedFrom returns a filter to find all machines that match a given EtcdadmCluster's infra template.
func MatchesTemplateClonedFrom(infraConfigs map[string]*unstructured.Unstructured, ec *etcdv1.EtcdadmCluster) collections.Func {
	return func(machine *clusterv1.Machine) bool {
		if machine == nil {
			return false
		}
		infraObj, found := infraConfigs[machine.Name]
		if !found {
			// Return true here because failing to get infrastructure machine should not be considered as unmatching.
			return true
		}

		clonedFromName, ok1 := infraObj.GetAnnotations()[clusterv1.TemplateClonedFromNameAnnotation]
		clonedFromGroupKind, ok2 := infraObj.GetAnnotations()[clusterv1.TemplateClonedFromGroupKindAnnotation]
		if !ok1 || !ok2 {
			// All etcdadmCluster cloned infra machines should have this annotation.
			// Missing the annotation may be due to older version machines or adopted machines.
			// Should not be considered as mismatch.
			return true
		}

		// Check if the machine's infrastructure reference has been created from the current etcdadmCluster infrastructure template.
		if clonedFromName != ec.Spec.InfrastructureTemplate.Name ||
			clonedFromGroupKind != ec.Spec.InfrastructureTemplate.GroupVersionKind().GroupKind().String() {
			return false
		}
		return true
	}
}

// getInfraResources fetches the external infrastructure resource for each machine in the collection and returns a map of machine.Name -> infraResource.
func getInfraResources(ctx context.Context, cl client.Client, machines collections.Machines) (map[string]*unstructured.Unstructured, error) {
	result := map[string]*unstructured.Unstructured{}
	for _, m := range machines {
		// Convert ContractVersionedObjectReference to ObjectReference
		// Use the APIGroup from the machine's InfrastructureRef and assume v1beta1 version
		apiVersion := m.Spec.InfrastructureRef.APIGroup + "/v1beta1"
		infraRef := &corev1.ObjectReference{
			APIVersion: apiVersion,
			Kind:       m.Spec.InfrastructureRef.Kind,
			Name:       m.Spec.InfrastructureRef.Name,
			Namespace:  m.Namespace,
		}
		infraObj, err := external.Get(ctx, cl, infraRef)
		if err != nil {
			if apierrors.IsNotFound(errors.Cause(err)) {
				continue
			}
			return nil, errors.Wrapf(err, "failed to retrieve infra obj for machine %q", m.Name)
		}
		result[m.Name] = infraObj
	}
	return result, nil
}

// getEtcdadmConfigs fetches the etcdadm config for each machine in the collection and returns a map of machine.Name -> EtcdadmConfig.
func getEtcdadmConfigs(ctx context.Context, cl client.Client, machines collections.Machines) (map[string]*etcdbootstrapv1.EtcdadmConfig, error) {
	result := map[string]*etcdbootstrapv1.EtcdadmConfig{}
	for _, m := range machines {
		bootstrapRef := m.Spec.Bootstrap.ConfigRef
		if !bootstrapRef.IsDefined() {
			continue
		}
		machineConfig := &etcdbootstrapv1.EtcdadmConfig{}
		if err := cl.Get(ctx, client.ObjectKey{Name: bootstrapRef.Name, Namespace: m.Namespace}, machineConfig); err != nil {
			if apierrors.IsNotFound(errors.Cause(err)) {
				continue
			}
			return nil, errors.Wrapf(err, "failed to retrieve bootstrap config for machine %q", m.Name)
		}
		result[m.Name] = machineConfig
	}
	return result, nil
}
