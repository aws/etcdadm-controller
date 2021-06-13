package controllers

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	etcdv1 "github.com/mrajashree/etcdadm-controller/api/v1alpha4"
	//etcdv1 "github.com/mrajashree/etcdadm-controller/api/v1alpha3"
	"github.com/pkg/errors"
	"net/http"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha4"
	//clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"
	"sigs.k8s.io/cluster-api/util/collections"
	ctrl "sigs.k8s.io/controller-runtime"
	"strings"
)

func (r *EtcdadmClusterReconciler) updateStatus(ctx context.Context, ec *etcdv1.EtcdadmCluster, cluster *clusterv1.Cluster) error {
	log := ctrl.LoggerFrom(ctx, "cluster", cluster.Name)
	log.Info("update status is called")
	selector := collections.EtcdPlaneSelectorForCluster(cluster.Name)
	// Copy label selector to its status counterpart in string format.
	// This is necessary for CRDs including scale subresources.
	ec.Status.Selector = selector.String()

	etcdMachines, err := collections.GetFilteredMachinesForCluster(ctx, r.Client, cluster, collections.EtcdClusterMachines(cluster.Name))
	if err != nil {
		return errors.Wrap(err, "Error filtering machines for etcd cluster")
	}
	ownedMachines := etcdMachines.Filter(collections.OwnedMachines(ec))
	log.Info("following machines owned by this etcd cluster:")
	for _, machine := range ownedMachines {
		fmt.Printf("%s ", machine.Name)
	}

	desiredReplicas := *ec.Spec.Replicas

	// set basic data that does not require interacting with the workload cluster
	ec.Status.ReadyReplicas = int32(len(ownedMachines))

	// Return early if the deletion timestamp is set, because we don't want to try to connect to the workload cluster
	// and we don't want to report resize condition (because it is set to deleting into reconcile delete).
	if !ec.DeletionTimestamp.IsZero() {
		return nil
	}

	if ec.Status.ReadyReplicas == desiredReplicas {
		var endpoint string
		for _, m := range ownedMachines {
			if len(m.Status.Addresses) == 0 {
				return nil
			}
			// TODO: save endpoint on the EtcdadmConfig status object
			if endpoint != "" {
				endpoint += ","
			}
			endpoint += fmt.Sprintf("https://%s:2379", getMachineAddress(m))
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
