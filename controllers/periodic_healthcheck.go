package controllers

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	etcdv1 "github.com/aws/etcdadm-controller/api/v1beta1"
	"github.com/hashicorp/go-multierror"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/collections"
	"sigs.k8s.io/cluster-api/util/conditions"
)

const (
	maxUnhealthyCount = 5
)

type etcdHealthCheckConfig struct {
	clusterToHttpClient sync.Map
}

type etcdadmClusterMemberHealthConfig struct {
	unhealthyMembersFrequency map[string]int
	unhealthyMembersToRemove  map[string]*clusterv1.Machine
	endpointToMachineMapper   map[string]*clusterv1.Machine
	cluster                   *clusterv1.Cluster
	endpoints                 string
	ownedMachines             collections.Machines
}

func (r *EtcdadmClusterReconciler) startHealthCheckLoop(ctx context.Context, done <-chan struct{}) {
	r.Log.Info("Starting periodic healthcheck loop")
	etcdadmClusterMapper := make(map[types.UID]etcdadmClusterMemberHealthConfig)
	ticker := time.NewTicker(r.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			r.startHealthCheck(ctx, etcdadmClusterMapper)
		}
	}
}

func (r *EtcdadmClusterReconciler) startHealthCheck(ctx context.Context, etcdadmClusterMapper map[types.UID]etcdadmClusterMemberHealthConfig) {
	etcdClusters := &etcdv1.EtcdadmClusterList{}
	err := r.Client.List(ctx, etcdClusters)
	if err != nil {
		r.Log.Error(err, "Error listing etcdadm cluster objects")
		return
	}
	for _, ec := range etcdClusters.Items {
		log := r.Log.WithValues("EtcdadmCluster", klog.KObj(&ec))
		if annotations.HasPaused(&ec) {
			log.Info("EtcdadmCluster reconciliation is paused, skipping health checks")
			continue
		}
		if val, set := ec.Annotations[etcdv1.HealthCheckRetriesAnnotation]; set {
			if retries, err := strconv.Atoi(val); err != nil || retries < 0 {
				log.Info(fmt.Sprintf("healthcheck-retries annotation configured with invalid value: %v", err))
			} else if retries == 0 {
				log.Info("healthcheck-retries annotation configured to 0, skipping health checks")
				continue
			}
		}
		if conditions.IsFalse(&ec, etcdv1.EtcdCertificatesAvailableCondition) {
			log.Info("EtcdadmCluster certificates are not ready, skipping health checks")
			continue
		}
		if !ec.Status.CreationComplete {
			// etcdCluster not fully provisioned yet
			log.Info("EtcdadmCluster is not ready, skipping health checks")
			continue
		}
		if conditions.IsFalse(&ec, etcdv1.EtcdMachinesSpecUpToDateCondition) {
			// etcdCluster is undergoing upgrade, some machines might not be ready yet, skip periodic healthcheck
			log.Info("EtcdadmCluster machine specs are not up to date, skipping health checks")
			continue
		}

		var cluster *clusterv1.Cluster
		if clusterEntry, ok := etcdadmClusterMapper[ec.UID]; !ok {
			cluster, err = util.GetOwnerCluster(ctx, r.Client, ec.ObjectMeta)
			if err != nil {
				log.Error(err, "Failed to retrieve owner Cluster from the API Server")
				continue
			}
			if cluster == nil {
				log.Info("Cluster Controller has not yet set OwnerRef on etcd cluster")
				continue
			}

			ownedMachines := r.getOwnedMachines(ctx, cluster, ec)
			endpointToMachineMapper := r.createEndpointToMachinesMap(ownedMachines)

			etcdadmClusterMapper[ec.UID] = etcdadmClusterMemberHealthConfig{
				unhealthyMembersFrequency: make(map[string]int),
				unhealthyMembersToRemove:  make(map[string]*clusterv1.Machine),
				endpointToMachineMapper:   endpointToMachineMapper,
				cluster:                   cluster,
				ownedMachines:             ownedMachines,
			}
		} else {
			cluster = clusterEntry.cluster
			if ec.Status.Endpoints != clusterEntry.endpoints {
				clusterEntry.endpoints = ec.Status.Endpoints
				ownedMachines := r.getOwnedMachines(ctx, cluster, ec)
				clusterEntry.ownedMachines = ownedMachines
				clusterEntry.endpointToMachineMapper = r.createEndpointToMachinesMap(ownedMachines)
				etcdadmClusterMapper[ec.UID] = clusterEntry
			}
		}

		if err := r.periodicEtcdMembersHealthCheck(ctx, cluster, &ec, etcdadmClusterMapper); err != nil {
			log.Error(err, "Error performing healthcheck")
			continue
		}
	}
}

func (r *EtcdadmClusterReconciler) periodicEtcdMembersHealthCheck(ctx context.Context, cluster *clusterv1.Cluster, etcdCluster *etcdv1.EtcdadmCluster, etcdadmClusterMapper map[types.UID]etcdadmClusterMemberHealthConfig) error {
	log := r.Log.WithValues("EtcdadmCluster", klog.KObj(etcdCluster))
	if len(etcdCluster.Status.Endpoints) == 0 {
		log.Info("Skipping healthcheck because Endpoints are empty", "Endpoints", etcdCluster.Status.Endpoints)
		return nil
	}
	currClusterHFConfig := etcdadmClusterMapper[etcdCluster.UID]
	endpoints := strings.Split(etcdCluster.Status.Endpoints, ",")
	for _, endpoint := range endpoints {
		err := r.performEndpointHealthCheck(ctx, cluster, endpoint, false)
		if err != nil {
			// member failed healthcheck so add it to unhealthy map or update it's unhealthy count
			log.Info("Member failed healthcheck, adding to unhealthy members list", "member", endpoint)
			currClusterHFConfig.unhealthyMembersFrequency[endpoint]++
			// if machine corresponding to the member does not exist, remove that member without waiting for max unhealthy count to be reached
			m, ok := currClusterHFConfig.endpointToMachineMapper[endpoint]
			if !ok || m == nil {
				log.Info("Machine for member does not exist", "member", endpoint)
				currClusterHFConfig.unhealthyMembersToRemove[endpoint] = m
			}
			unhealthyCount := maxUnhealthyCount
			if val, set := etcdCluster.Annotations[etcdv1.HealthCheckRetriesAnnotation]; set {
				retries, err := strconv.Atoi(val)
				if err != nil || retries < 0 {
					log.Info("healthcheck-retries annotation configured with invalid value, using default retries")
				}
				unhealthyCount = retries
			}
			if currClusterHFConfig.unhealthyMembersFrequency[endpoint] >= unhealthyCount {
				log.Info("Adding to list of unhealthy members to remove", "member", endpoint)
				// member has been unresponsive, add the machine to unhealthyMembersToRemove queue
				m := currClusterHFConfig.endpointToMachineMapper[endpoint]
				currClusterHFConfig.unhealthyMembersToRemove[endpoint] = m
			}
		} else {
			// member passed healthcheck. so if it was previously added to unhealthy map, remove it since only consecutive failures should lead to member removal
			_, markedUnhealthy := currClusterHFConfig.unhealthyMembersFrequency[endpoint]
			if markedUnhealthy {
				delete(currClusterHFConfig.unhealthyMembersFrequency, endpoint)
			}
		}
	}

	if len(currClusterHFConfig.unhealthyMembersToRemove) == 0 {
		return nil
	}

	var retErr error
	for machineEndpoint, machineToDelete := range currClusterHFConfig.unhealthyMembersToRemove {
		if err := r.removeEtcdMachine(ctx, etcdCluster, cluster, machineToDelete, getEtcdMachineAddressFromClientURL(machineEndpoint)); err != nil {
			// log and save error and continue deletion of other members, deletion of this member will be retried since it's still part of unhealthyMembersToRemove
			if machineToDelete == nil {
				log.Error(err, "error removing etcd member machine, machine not found", "endpoint", machineEndpoint)
			} else {
				log.Error(err, "error removing etcd member machine", "member", machineToDelete.Name, "endpoint", machineEndpoint)
			}
			retErr = multierror.Append(retErr, err)
			continue
		}
		delete(currClusterHFConfig.unhealthyMembersToRemove, machineEndpoint)
	}
	if retErr != nil {
		return retErr
	}

	etcdCluster.Status.Ready = false
	return r.Client.Status().Update(ctx, etcdCluster)
}

func (r *EtcdadmClusterReconciler) createEndpointToMachinesMap(ownedMachines collections.Machines) map[string]*clusterv1.Machine {
	endpointToMachineMapper := make(map[string]*clusterv1.Machine)
	for _, m := range ownedMachines {
		machineClientURL := getMemberClientURL(getEtcdMachineAddress(m))
		endpointToMachineMapper[machineClientURL] = m
	}
	return endpointToMachineMapper
}

func (r *EtcdadmClusterReconciler) getOwnedMachines(ctx context.Context, cluster *clusterv1.Cluster, ec etcdv1.EtcdadmCluster) collections.Machines {
	etcdMachines, err := collections.GetFilteredMachinesForCluster(ctx, r.uncachedClient, cluster, EtcdClusterMachines(cluster.Name, ec.Name))
	if err != nil {
		r.Log.Error(err, "Error filtering machines for etcd cluster")
	}

	return etcdMachines.Filter(collections.OwnedMachines(&ec))
}
