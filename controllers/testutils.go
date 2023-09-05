package controllers

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"

	etcdbootstrapv1 "github.com/aws/etcdadm-bootstrap-provider/api/v1beta1"
	etcdv1 "github.com/aws/etcdadm-controller/api/v1beta1"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/utils/pointer"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	testClusterName                = "testCluster"
	testNamespace                  = "test"
	testEtcdadmClusterName         = "testEtcdadmCluster"
	testInfrastructureTemplateName = "testInfraTemplate"
	etcdClusterNameSuffix          = "etcd-cluster"
	etcdVersion                    = "v3.4.9"
)

var (
	infraTemplate = &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind":       "InfrastructureTemplate",
			"apiVersion": "infra.io/v1",
			"metadata": map[string]interface{}{
				"name":      testInfrastructureTemplateName,
				"namespace": testNamespace,
			},
			"spec": map[string]interface{}{
				"template": map[string]interface{}{
					"spec": map[string]interface{}{
						"hello": "world",
					},
				},
			},
		},
	}

	healthyEtcdResponse = &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString("{\"Health\": \"true\"}")),
	}
)

type etcdadmClusterTest struct {
	replicas       int
	name           string
	namespace      string
	cluster        *clusterv1.Cluster
	etcdadmCluster *etcdv1.EtcdadmCluster
	machines       []*clusterv1.Machine
}

func newEtcdadmClusterTest() *etcdadmClusterTest {
	return &etcdadmClusterTest{
		name:      testClusterName,
		namespace: testNamespace,
		replicas:  3,
	}
}

func (e *etcdadmClusterTest) buildClusterWithExternalEtcd() {
	e.cluster = e.newClusterWithExternalEtcd()
	e.etcdadmCluster = e.newEtcdadmCluster(e.cluster)
	e.machines = []*clusterv1.Machine{}
	endpoints := []string{}
	for i := 0; i < e.replicas; i++ {
		machine := e.newEtcdMachine()
		e.machines = append(e.machines, machine)
		endpoints = append(endpoints, fmt.Sprintf("https://%v:2379", machine.Status.Addresses[0].Address))
	}
	e.etcdadmCluster.Status.Endpoints = strings.Join(endpoints, ",")
}

// newClusterWithExternalEtcd return a CAPI cluster object with managed external etcd ref
func (e *etcdadmClusterTest) newClusterWithExternalEtcd() *clusterv1.Cluster {
	return &clusterv1.Cluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Cluster",
			APIVersion: clusterv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: e.namespace,
			Name:      e.name,
			UID:       types.UID(uuid.New().String()),
		},
		Spec: clusterv1.ClusterSpec{
			ManagedExternalEtcdRef: &corev1.ObjectReference{
				Kind:       "EtcdadmCluster",
				Namespace:  e.namespace,
				Name:       e.name,
				APIVersion: etcdv1.GroupVersion.String(),
			},
			InfrastructureRef: &corev1.ObjectReference{
				Kind:       "InfrastructureTemplate",
				Namespace:  e.namespace,
				Name:       testInfrastructureTemplateName,
				APIVersion: "infra.io/v1",
			},
		},
		Status: clusterv1.ClusterStatus{
			InfrastructureReady: true,
		},
	}
}

func (e *etcdadmClusterTest) newEtcdadmCluster(cluster *clusterv1.Cluster) *etcdv1.EtcdadmCluster {
	return &etcdv1.EtcdadmCluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "EtcdadmCluster",
			APIVersion: etcdv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: e.namespace,
			Name:      e.getEtcdClusterName(),
			UID:       types.UID(uuid.New().String()),
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(e.cluster, clusterv1.GroupVersion.WithKind("Cluster")),
			},
			Finalizers: []string{etcdv1.EtcdadmClusterFinalizer},
		},
		Spec: etcdv1.EtcdadmClusterSpec{
			EtcdadmConfigSpec: etcdbootstrapv1.EtcdadmConfigSpec{
				CloudInitConfig: &etcdbootstrapv1.CloudInitConfig{
					Version: etcdVersion,
				},
			},
			Replicas: pointer.Int32(int32(e.replicas)),
			InfrastructureTemplate: corev1.ObjectReference{
				Kind:       infraTemplate.GetKind(),
				APIVersion: infraTemplate.GetAPIVersion(),
				Name:       infraTemplate.GetName(),
				Namespace:  e.namespace,
			},
		},
	}
}

func (e *etcdadmClusterTest) newEtcdMachine() *clusterv1.Machine {
	return &clusterv1.Machine{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Machine",
			APIVersion: clusterv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.SimpleNameGenerator.GenerateName(e.etcdadmCluster.Name + "-"),
			Namespace: e.etcdadmCluster.Namespace,
			Labels:    EtcdLabelsForCluster(e.cluster.Name, e.etcdadmCluster.Name),
			UID:       types.UID(uuid.New().String()),
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(e.etcdadmCluster, etcdv1.GroupVersion.WithKind("EtcdadmCluster")),
			},
		},
		Spec: clusterv1.MachineSpec{
			ClusterName: e.cluster.Name,
			InfrastructureRef: corev1.ObjectReference{
				Kind:       infraTemplate.GetKind(),
				APIVersion: infraTemplate.GetAPIVersion(),
				Name:       infraTemplate.GetName(),
				Namespace:  infraTemplate.GetNamespace(),
			},
		},
		Status: clusterv1.MachineStatus{
			Addresses: []clusterv1.MachineAddress{
				{
					Type:    clusterv1.MachineExternalIP,
					Address: fmt.Sprintf("%d.%d.%d.%d", rand.Intn(256), rand.Intn(256), rand.Intn(256), rand.Intn(256)),
				},
			},
		},
	}
}

func (e *etcdadmClusterTest) gatherObjects() []client.Object {
	objects := []client.Object{e.cluster, e.etcdadmCluster}
	for _, machine := range e.machines {
		objects = append(objects, machine)
	}
	return objects
}

func (e *etcdadmClusterTest) getEtcdClusterName() string {
	return fmt.Sprintf("%s-%s", e.name, etcdClusterNameSuffix)
}
