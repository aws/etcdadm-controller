package controllers

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"time"

	etcdv1 "github.com/mrajashree/etcdadm-controller/api/v1alpha3"
	"github.com/pkg/errors"
	clientv3 "go.etcd.io/etcd/client/v3"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"
	"sigs.k8s.io/cluster-api/util/collections"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/etcdadm/constants"
)

func (r *EtcdadmClusterReconciler) intializeEtcdCluster(ctx context.Context, ec *etcdv1.EtcdadmCluster, cluster *clusterv1.Cluster, ep *EtcdPlane) (ctrl.Result, error) {
	if err := r.generateCAandClientCertSecrets(ctx, cluster, ec); err != nil {
		r.Log.Error(err, "error generating etcd CA certs")
		return ctrl.Result{}, err
	}
	conditions.MarkTrue(ec, etcdv1.EtcdCertificatesAvailableCondition)
	fd := ep.NextFailureDomainForScaleUp()
	return r.cloneConfigsAndGenerateMachine(ctx, ec, cluster, fd)
}

func (r *EtcdadmClusterReconciler) scaleUpEtcdCluster(ctx context.Context, ec *etcdv1.EtcdadmCluster, cluster *clusterv1.Cluster, ep *EtcdPlane) (ctrl.Result, error) {
	fd := ep.NextFailureDomainForScaleUp()
	return r.cloneConfigsAndGenerateMachine(ctx, ec, cluster, fd)
}

func (r *EtcdadmClusterReconciler) scaleDownEtcdCluster(ctx context.Context, ec *etcdv1.EtcdadmCluster, cluster *clusterv1.Cluster, ep *EtcdPlane, outdatedMachines collections.FilterableMachineCollection) (ctrl.Result, error) {
	log := r.Log
	// Pick the Machine that we should scale down.
	machineToDelete, err := selectMachineForScaleDown(ep, outdatedMachines)
	if err != nil || machineToDelete == nil {
		return ctrl.Result{}, errors.Wrap(err, "failed to select machine for scale down")
	}

	// Etcdadm has a "reset" command to remove an etcd member. But we can't run that command on the CAPI machine object after it's provisioned.
	// so the following logic is based on how etcdadm performs "reset" https://github.com/kubernetes-sigs/etcdadm/blob/master/cmd/reset.go#L65
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

	// TODO: save endpoint on the EtcdadmConfig status object
	machineAddress := getEtcdMachineAddress(machineToDelete)
	endpoint := fmt.Sprintf("https://%s:2379", machineAddress)
	peerURL := fmt.Sprintf("https://%s:2380", machineAddress)
	if err := r.changeClusterInitAddress(ctx, ec, cluster, ep, machineAddress, machineToDelete); err != nil {
		return ctrl.Result{}, err
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
		log.Info(" Member was not removed")
		if len(mresp.Members) > 1 {
			log.Info("Removing member")
			etcdCtx, cancel = context.WithTimeout(ctx, constants.DefaultEtcdRequestTimeout)
			_, err = etcdClient.MemberRemove(etcdCtx, localMember.ID)
			cancel()
			if err != nil {
				log.Error(err, "Error removing member: %v")
			}
			if err := r.Client.Delete(ctx, machineToDelete); err != nil && !apierrors.IsNotFound(err) {
				log.Error(err, "Failed to delete etcd machine")
				return ctrl.Result{}, err
			}
		} else {
			log.Info("Not removing member because it is the last in the cluster")
		}
	} else {
		log.Info("Member was removed")
	}

	return ctrl.Result{}, nil
}
