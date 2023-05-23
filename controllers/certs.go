package controllers

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"sigs.k8s.io/cluster-api/util/conditions"

	"path/filepath"

	etcdv1 "github.com/aws/etcdadm-controller/api/v1beta1"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	certutil "k8s.io/client-go/util/cert"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/certs"
	"sigs.k8s.io/cluster-api/util/secret"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/etcdadm/certs/pkiutil"
	"sigs.k8s.io/etcdadm/constants"
)

// etcdadm provisioning works as follows:
// machine one runs etcdadm init, generates CA and client certs
// CA certs are copied over to remaining nodes to run etcdadm join
// This provider is going to generate CA cert-key for etcd, and create two Secrets to store CA cert + client cert-key to be used by kube-apiserver
func (r *EtcdadmClusterReconciler) generateCAandClientCertSecrets(ctx context.Context, cluster *clusterv1.Cluster, etcdCluster *etcdv1.EtcdadmCluster) error {
	log := r.Log
	// Generate external etcd CA cert + key pair
	CACertKeyPair := etcdCACertKeyPair()
	err := CACertKeyPair.LookupOrGenerate(
		ctx,
		r.Client,
		util.ObjectKey(cluster),
		*metav1.NewControllerRef(etcdCluster, etcdv1.GroupVersion.WithKind("EtcdadmCluster")),
	)
	if err != nil {
		log.Error(err, "Failed to look up or generate CA cert key pair")
		return err
	}

	caCertKey := CACertKeyPair.GetByPurpose(secret.ManagedExternalEtcdCA)
	if caCertKey == nil {
		return fmt.Errorf("nil returned from getting etcd CA certificate by purpose %s", secret.ManagedExternalEtcdCA)
	}

	// Use the generated CA cert+key pair to generate and sign etcd client cert+key pair
	caCertDecoded, _ := pem.Decode(caCertKey.KeyPair.Cert)
	caCert, err := x509.ParseCertificate(caCertDecoded.Bytes)
	if err != nil {
		log.Error(err, "Failed to parse etcd CA cert")
		return err
	}
	caKeyDecoded, _ := pem.Decode(caCertKey.KeyPair.Key)
	caKey, err := x509.ParsePKCS1PrivateKey(caKeyDecoded.Bytes)
	if err != nil {
		log.Error(err, "Failed to parse etcd CA key")
		return err
	}

	commonName := fmt.Sprintf("%s-kube-apiserver-etcd-client", cluster.Name)
	// This certConfig is what etcdadm uses to generate client certs https://github.com/kubernetes-sigs/etcdadm/blob/master/certs/certs.go#L233
	certConfig := certutil.Config{
		CommonName:   commonName,
		Organization: []string{constants.MastersGroup},
		Usages:       []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	apiClientCert, apiClientKey, err := pkiutil.NewCertAndKey(caCert, caKey, certConfig)
	if err != nil {
		return fmt.Errorf("failure while creating %q etcd client key and certificate: %v", commonName, err)
	}

	// Now generate two Secrets, one containing the client cert+key pair and other containing the etcd CA cert. Ech control plane provider should
	// use these two Secrets for communicating with etcd.
	apiServerClientCertKeyPair := secret.Certificate{
		Purpose: secret.APIServerEtcdClient,
		KeyPair: &certs.KeyPair{
			Cert: certs.EncodeCertPEM(apiClientCert),
			Key:  certs.EncodePrivateKeyPEM(apiClientKey),
		},
		Generated: true,
	}
	s := apiServerClientCertKeyPair.AsSecret(client.ObjectKey{Name: cluster.Name, Namespace: cluster.Namespace}, *metav1.NewControllerRef(etcdCluster, etcdv1.GroupVersion.WithKind("EtcdadmCluster")))
	if err := r.Client.Create(ctx, s); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failure while saving etcd client key and certificate: %v", err)
	}

	log.Info("Saved apiserver client cert key as secret")

	s = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace,
			Name:      secret.Name(cluster.Name, secret.EtcdCA),
			Labels: map[string]string{
				clusterv1.ClusterNameLabel: cluster.Name,
			},
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(etcdCluster, etcdv1.GroupVersion.WithKind("EtcdadmCluster"))},
		},
		Data: map[string][]byte{
			secret.TLSCrtDataName: caCertKey.KeyPair.Cert,
		},
		Type: clusterv1.ClusterSecretType,
	}
	if err := r.Client.Create(ctx, s); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failure while saving etcd CA certificate: %v", err)
	}

	log.Info("Saved etcd ca cert as secret")
	conditions.MarkTrue(etcdCluster, etcdv1.EtcdCertificatesAvailableCondition)
	return nil
}

func etcdCACertKeyPair() secret.Certificates {
	certificatesDir := "/etc/etcd/pki"
	certificates := secret.Certificates{
		&secret.Certificate{
			Purpose:  secret.ManagedExternalEtcdCA,
			CertFile: filepath.Join(certificatesDir, "ca.crt"),
			KeyFile:  filepath.Join(certificatesDir, "ca.key"),
		},
	}

	return certificates
}

// TODO: save CA and client cert on the reconciler object
func (r *EtcdadmClusterReconciler) getCACert(ctx context.Context, cluster *clusterv1.Cluster) ([]byte, error) {
	caCert := &secret.Certificates{
		&secret.Certificate{
			Purpose: secret.ManagedExternalEtcdCA,
		},
	}
	if err := caCert.Lookup(ctx, r.Client, util.ObjectKey(cluster)); err != nil {
		return []byte{}, errors.Wrap(err, "error looking up external etcd CA certs")
	}
	if caCertKey := caCert.GetByPurpose(secret.ManagedExternalEtcdCA); caCertKey != nil {
		return caCertKey.KeyPair.Cert, nil
	}
	return []byte{}, fmt.Errorf("nil returned from getting etcd CA certificate by purpose %s", secret.ManagedExternalEtcdCA)
}

func (r *EtcdadmClusterReconciler) getClientCerts(ctx context.Context, cluster *clusterv1.Cluster) (tls.Certificate, error) {
	clientCert := &secret.Certificates{
		&secret.Certificate{
			Purpose: secret.APIServerEtcdClient,
		},
	}
	if err := clientCert.Lookup(ctx, r.Client, util.ObjectKey(cluster)); err != nil {
		return tls.Certificate{}, err
	}
	if clientCertKey := clientCert.GetByPurpose(secret.APIServerEtcdClient); clientCertKey != nil {
		return tls.X509KeyPair(clientCertKey.KeyPair.Cert, clientCertKey.KeyPair.Key)
	}
	return tls.Certificate{}, fmt.Errorf("nil returned from getting etcd CA certificate by purpose %s", secret.APIServerEtcdClient)
}
