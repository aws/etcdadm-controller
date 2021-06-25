package controllers

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"strings"

	etcdv1 "github.com/mrajashree/etcdadm-controller/api/v1alpha3"
	"github.com/pkg/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/collections"
)

func (r *EtcdadmClusterReconciler) updateStatus(ctx context.Context, ec *etcdv1.EtcdadmCluster, cluster *clusterv1.Cluster) error {
	log := r.Log.WithName(ec.Name)
	log.Info("Updating etcd cluster status")
	selector := EtcdPlaneSelectorForCluster(cluster.Name)
	// Copy label selector to its status counterpart in string format.
	// This is necessary for CRDs including scale subresources.
	ec.Status.Selector = selector.String()

	etcdMachines, err := collections.GetMachinesForCluster(ctx, r.Client, util.ObjectKey(cluster), EtcdClusterMachines(cluster.Name))
	if err != nil {
		return errors.Wrap(err, "Error filtering machines for etcd cluster")
	}
	ownedMachines := etcdMachines.Filter(collections.OwnedMachines(ec))
	log.Info(fmt.Sprintf("following machines are owned by this etcd cluster: %v", ownedMachines.Names()))

	desiredReplicas := *ec.Spec.Replicas

	log.Info(fmt.Sprintf("ready replicas for etcd cluster %v: %v", ec.Name, ec.Status.ReadyReplicas))

	if !ec.DeletionTimestamp.IsZero() {
		return nil
	}

	if ec.Status.ReadyReplicas == desiredReplicas {
		log.Info("Performing endpoint healthcheck and updating fields")
		var endpoint string
		for _, m := range ownedMachines {
			log.Info(fmt.Sprintf("Performing healthcheck for machine %v", m.Name))
			if len(m.Status.Addresses) == 0 {
				return nil
			}
			// TODO: save endpoint on the EtcdadmConfig status object
			if endpoint != "" {
				endpoint += ","
			}
			endpoint += fmt.Sprintf("https://%s:2379", getEtcdMachineAddress(m))
		}
		log.Info(fmt.Sprintf("running endpoint checks on %v", endpoint))
		if err := r.doEtcdHealthCheck(ctx, cluster, endpoint); err != nil {
			return err
		}
		// etcd ready when all machines have address set
		ec.Status.CreationComplete = true
		ec.Status.Endpoint = endpoint
	}
	return nil
}

func (r *EtcdadmClusterReconciler) doEtcdHealthCheck(ctx context.Context, cluster *clusterv1.Cluster, endpoints string) error {
	caCertPool := x509.NewCertPool()
	caCert, err := r.getCACert(ctx, cluster)
	if err != nil {
		return err
	}
	caCertPool.AppendCertsFromPEM(caCert)

	clientCert, err := r.getClientCerts(ctx, cluster)
	if err != nil {
		return errors.Wrap(err, "Error getting client cert for healthcheck")
	}
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:      caCertPool,
				Certificates: []tls.Certificate{clientCert},
			},
		},
	}

	for _, endpoint := range strings.Split(endpoints, ",") {
		req, err := http.NewRequest("GET", endpoint+"/health", nil)
		if err != nil {
			return err
		}
		req.Close = true

		resp, err := client.Do(req)
		if err != nil {
			return errors.Wrap(err, "error checking etcd member health")
		}

		if resp.StatusCode != http.StatusOK {
			return errors.Wrap(err, "error member not ready, retry")
		}
		r.Log.Info(fmt.Sprintf("Etcd member %v ready", endpoint+"/health"))
	}
	return nil
}
