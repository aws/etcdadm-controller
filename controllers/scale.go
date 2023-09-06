package controllers

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"strings"
	"time"

	etcdv1 "github.com/aws/etcdadm-controller/api/v1beta1"
	"github.com/pkg/errors"
	clientv3 "go.etcd.io/etcd/client/v3"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/collections"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/etcdadm/constants"
)

const etcdClientTimeout = 5 * time.Second

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

func (r *EtcdadmClusterReconciler) scaleDownEtcdCluster(ctx context.Context, ec *etcdv1.EtcdadmCluster, cluster *clusterv1.Cluster, ep *EtcdPlane, outdatedMachines collections.Machines) (ctrl.Result, error) {
	// Pick the Machine that we should scale down.
	machineToDelete, err := selectMachineForScaleDown(ep, outdatedMachines)
	if err != nil || machineToDelete == nil {
		return ctrl.Result{}, errors.Wrap(err, "failed to select machine for scale down")
	}
	machineAddress := getEtcdMachineAddress(machineToDelete)
	return ctrl.Result{}, r.removeEtcdMachine(ctx, ec, cluster, machineToDelete, machineAddress)
}

func (r *EtcdadmClusterReconciler) removeEtcdMachine(ctx context.Context, ec *etcdv1.EtcdadmCluster, cluster *clusterv1.Cluster, machineToDelete *clusterv1.Machine, machineAddress string) error {
	peerURL := fmt.Sprintf("https://%s:2380", machineAddress)
	etcdClient, err := r.GetEtcdClient(ctx, cluster, ec.Status.Endpoints)
	if err != nil {
		return fmt.Errorf("error creating etcd client, err: %v", err)
	}
	if etcdClient == nil {
		return fmt.Errorf("could not create etcd client")
	}
	defer etcdClient.Close()

	return r.removeEtcdMemberAndDeleteMachine(ctx, etcdClient, peerURL, machineToDelete)

}

func (r *EtcdadmClusterReconciler) generateEtcdClient(ctx context.Context, cluster *clusterv1.Cluster, endpoints string) (EtcdClient, error) {
	caCertPool := x509.NewCertPool()
	caCert, err := r.getCACert(ctx, cluster)
	if err != nil {
		return nil, err
	}
	caCertPool.AppendCertsFromPEM(caCert)

	clientCert, err := r.getClientCerts(ctx, cluster)
	if err != nil {
		return nil, errors.Wrap(err, "error getting client cert for healthcheck")
	}

	etcdClient, err := clientv3.New(clientv3.Config{
		Endpoints:   strings.Split(endpoints, ","),
		DialTimeout: etcdClientTimeout,
		TLS: &tls.Config{
			RootCAs:      caCertPool,
			Certificates: []tls.Certificate{clientCert},
		},
	})

	return etcdClient, err
}

func (r *EtcdadmClusterReconciler) removeEtcdMemberAndDeleteMachine(ctx context.Context, etcdClient EtcdClient, peerURL string, machineToDelete *clusterv1.Machine) error {
	log := r.Log
	// Etcdadm has a "reset" command to remove an etcd member. But we can't run that command on the CAPI machine object after it's provisioned.
	// so the following logic is based on how etcdadm performs "reset" https://github.com/kubernetes-sigs/etcdadm/blob/master/cmd/reset.go#L65
	etcdCtx, cancel := context.WithTimeout(ctx, constants.DefaultEtcdRequestTimeout)
	mresp, err := etcdClient.MemberList(etcdCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("error listing members: %v", err)
	}
	localMember, ok := memberForPeerURLs(mresp, []string{peerURL})
	if ok {
		if len(mresp.Members) > 1 {
			log.Info("Removing", "member", localMember.Name)
			etcdCtx, cancel = context.WithTimeout(ctx, constants.DefaultEtcdRequestTimeout)
			_, err = etcdClient.MemberRemove(etcdCtx, localMember.ID)
			cancel()
			if err != nil {
				return fmt.Errorf("failed to remove etcd member %s with error %v", localMember.Name, err)
			}
			if machineToDelete != nil {
				if err := r.Client.Delete(ctx, machineToDelete); err != nil && !apierrors.IsNotFound(err) && !apierrors.IsGone(err) {
					return fmt.Errorf("failed to delete etcd machine %s with error %v", machineToDelete.Name, err)
				}
			}
		} else {
			log.Info("Not removing last member in the cluster", "member", localMember.Name)
		}
	} else {
		log.Info("Member was removed")
		if machineToDelete != nil {
			// this could happen if the etcd member was removed through etcdctl calls, ensure that the machine gets deleted too
			if err := r.Client.Delete(ctx, machineToDelete); err != nil && !apierrors.IsNotFound(err) && !apierrors.IsGone(err) {
				return fmt.Errorf("failed to delete etcd machine %s with error %v", machineToDelete.Name, err)
			}
		}
	}
	return nil
}
