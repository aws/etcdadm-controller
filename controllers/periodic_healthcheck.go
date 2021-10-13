package controllers

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-multierror"
	etcdv1 "github.com/mrajashree/etcdadm-controller/api/v1alpha3"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/collections"
	"sigs.k8s.io/cluster-api/util/conditions"
)

const (
	maxUnhealthyCount   = 5
	healthCheckInterval = 30
)

type etcdHealthCheckConfig struct {
	clusterToHttpClient sync.Map
}

type etcdadmClusterMemberHealthConfig struct {
	unhealthyMembersFrequency map[string]int
	unhealthyMembersToRemove  map[string]*clusterv1.Machine
	endpointToMachineMapper   map[string]*clusterv1.Machine
	cluster                   *clusterv1.Cluster
	ownedMachines             collections.FilterableMachineCollection
}

func (r *EtcdadmClusterReconciler) startHealthCheckLoop(ctx context.Context, done <-chan struct{}) {
	r.Log.Info("Starting periodic healthcheck loop")
	etcdadmClusterMapper := make(map[types.UID]etcdadmClusterMemberHealthConfig)
	ticker := time.NewTicker(healthCheckInterval * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			etcdClusters := &etcdv1.EtcdadmClusterList{}
			err := r.Client.List(ctx, etcdClusters)
			if err != nil {
				r.Log.Error(err, "Error listing etcdadm cluster objects")
				continue
			}
			for _, ec := range etcdClusters.Items {
				if !ec.Status.CreationComplete {
					// etcdCluster not fully provisioned yet
					continue
				}
				if conditions.IsFalse(&ec, etcdv1.EtcdMachinesSpecUpToDateCondition) {
					// etcdCluster is undergoing upgrade, some machines might not be ready yet, skip periodic healthcheck
					continue
				}

				var cluster *clusterv1.Cluster
				if clusterEntry, ok := etcdadmClusterMapper[ec.UID]; !ok {
					cluster, err = util.GetOwnerCluster(ctx, r.Client, ec.ObjectMeta)
					if err != nil {
						r.Log.Error(err, "Failed to retrieve owner Cluster from the API Server")
						continue
					}
					if cluster == nil {
						r.Log.Info("Cluster Controller has not yet set OwnerRef on etcd cluster")
						continue
					}
					etcdMachines, err := collections.GetMachinesForCluster(ctx, r.uncachedClient, util.ObjectKey(cluster), EtcdClusterMachines(cluster.Name, ec.Name))
					if err != nil {
						r.Log.Error(err, "Error filtering machines for etcd cluster")
					}

					ownedMachines := etcdMachines.Filter(collections.OwnedMachines(&ec))
					endpointToMachineMapper := make(map[string]*clusterv1.Machine)
					for _, m := range ownedMachines {
						machineClientURL := getMemberClientURL(getEtcdMachineAddress(m))
						endpointToMachineMapper[machineClientURL] = m
					}

					etcdadmClusterMapper[ec.UID] = etcdadmClusterMemberHealthConfig{
						unhealthyMembersFrequency: make(map[string]int),
						unhealthyMembersToRemove:  make(map[string]*clusterv1.Machine),
						endpointToMachineMapper:   endpointToMachineMapper,
						cluster:                   cluster,
						ownedMachines:             ownedMachines,
					}
				} else {
					cluster = clusterEntry.cluster
				}

				if err := r.periodicEtcdMembersHealthCheck(ctx, cluster, &ec, etcdadmClusterMapper); err != nil {
					r.Log.Error(err, "Error performing healthcheck")
					continue
				}
			}
		}
	}
}

func (r *EtcdadmClusterReconciler) periodicEtcdMembersHealthCheck(ctx context.Context, cluster *clusterv1.Cluster, etcdCluster *etcdv1.EtcdadmCluster, etcdadmClusterMapper map[types.UID]etcdadmClusterMemberHealthConfig) error {
	currClusterHFConfig := etcdadmClusterMapper[etcdCluster.UID]
	endpoints := strings.Split(etcdCluster.Status.Endpoints, ",")
	for _, endpoint := range endpoints {
		err := r.performEndpointHealthCheck(ctx, cluster, endpoint, false)
		if err != nil {
			// member failed healthcheck so add it to unhealthy map or update it's unhealthy count
			currClusterHFConfig.unhealthyMembersFrequency[endpoint]++
			r.Log.Info("Member failed healthcheck, adding to unhealthy members list", "member", endpoint)
			if currClusterHFConfig.unhealthyMembersFrequency[endpoint] >= maxUnhealthyCount {
				r.Log.Info("Adding to list of unhealthy members to remove", "member", endpoint)
				// member has been unresponsive, add the machine to unhealthyMembersToRemove queue
				m := currClusterHFConfig.endpointToMachineMapper[endpoint]
				currClusterHFConfig.unhealthyMembersToRemove[endpoint] = m
			}
		} else {
			// member passed healthcheck. so if it was previously added to unhealthy map, remove it since only consecutive failures should lead to member removal
			if _, markedUnhealthy := currClusterHFConfig.unhealthyMembersFrequency[endpoint]; markedUnhealthy {
				delete(currClusterHFConfig.unhealthyMembersFrequency, endpoint)
			}
		}
	}

	if len(currClusterHFConfig.unhealthyMembersToRemove) == 0 {
		return nil
	}

	finalEndpoints := make([]string, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if _, existsInUnhealthyMap := currClusterHFConfig.unhealthyMembersToRemove[endpoint]; !existsInUnhealthyMap {
			finalEndpoints = append(finalEndpoints, endpoint)
		}
	}

	ep, err := NewEtcdPlane(ctx, r.Client, currClusterHFConfig.cluster, etcdCluster, currClusterHFConfig.ownedMachines)
	if err != nil {
		return errors.Wrap(err, "Error initializing internal object EtcdPlane")
	}

	var retErr error
	for machineEndpoint, machineToDelete := range currClusterHFConfig.unhealthyMembersToRemove {
		if err := r.removeEtcdMember(ctx, etcdCluster, cluster, ep, machineToDelete); err != nil {
			// log and save error and continue deletion of other members, deletion of this member will be retried since it's still part of unhealthyMembersToRemove
			r.Log.Error(err, fmt.Sprintf("error removing etcd member machine %v", machineToDelete.Name))
			retErr = multierror.Append(retErr, err)
			continue
		}
		delete(currClusterHFConfig.unhealthyMembersToRemove, machineEndpoint)
	}
	if retErr != nil {
		return retErr
	}

	etcdCluster.Status.Endpoints = strings.Join(finalEndpoints, ",")
	etcdCluster.Status.Ready = false
	return r.Client.Status().Update(ctx, etcdCluster)
}
