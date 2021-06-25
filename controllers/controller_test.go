/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"testing"

	bootstrapv1alpha3 "github.com/mrajashree/etcdadm-bootstrap-provider/api/v1alpha3"
	etcdv1 "github.com/mrajashree/etcdadm-controller/api/v1alpha3"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/utils/pointer"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"
	"sigs.k8s.io/cluster-api/test/helpers"
	"sigs.k8s.io/cluster-api/util"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var cfg *rest.Config
var k8sClient client.Client
var testEnv *helpers.TestEnvironment

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Controller Suite",
		[]Reporter{printer.NewlineReporter{}})
}

func setupScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	if err := clusterv1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := etcdv1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := bootstrapv1alpha3.AddToScheme(scheme); err != nil {
		panic(err)
	}
	return scheme
}

const (
	testClusterName                = "testCluster"
	testNamespace                  = "test"
	testEtcdadmClusterName         = "testEtcdadmCluster"
	testInfrastructureTemplateName = "testInfraTemplate"
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
)

func TestClusterToEtcdadmCluster(t *testing.T) {
	g := NewWithT(t)

	cluster := newCluster()
	cluster.Spec = clusterv1.ClusterSpec{
		ManagedExternalEtcdRef: &corev1.ObjectReference{
			Kind:       "EtcdadmCluster",
			Namespace:  "default",
			Name:       "etcdadmCluster",
			APIVersion: etcdv1.GroupVersion.String(),
		},
	}

	objects := []runtime.Object{
		cluster,
	}
	fakeClient := helpers.NewFakeClientWithScheme(setupScheme(), objects...)

	expectedResult := []ctrl.Request{
		{
			NamespacedName: client.ObjectKey{
				Namespace: cluster.Spec.ManagedExternalEtcdRef.Namespace,
				Name:      cluster.Spec.ManagedExternalEtcdRef.Name},
		},
	}

	r := &EtcdadmClusterReconciler{
		Client: fakeClient,
		Log:    log.Log,
	}

	got := r.ClusterToEtcdadmCluster(
		handler.MapObject{
			Meta:   cluster.GetObjectMeta(),
			Object: cluster,
		},
	)
	g.Expect(got).To(Equal(expectedResult))
}

func TestReconcileNoClusterOwnerRef(t *testing.T) {
	g := NewWithT(t)

	etcdadmCluster := &etcdv1.EtcdadmCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      testEtcdadmClusterName,
		},
		Spec: etcdv1.EtcdadmClusterSpec{
			Version: "v3.4.9",
		},
	}

	objects := []runtime.Object{
		etcdadmCluster,
	}
	fakeClient := helpers.NewFakeClientWithScheme(setupScheme(), objects...)

	r := &EtcdadmClusterReconciler{
		Client: fakeClient,
		Log:    log.Log,
	}

	result, err := r.Reconcile(ctrl.Request{NamespacedName: util.ObjectKey(etcdadmCluster)})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(result).To(Equal(ctrl.Result{}))

	machineList := &clusterv1.MachineList{}
	g.Expect(fakeClient.List(context.Background(), machineList, client.InNamespace("test"))).To(Succeed())
	g.Expect(machineList.Items).To(BeEmpty())
}

func TestReconcilePaused(t *testing.T) {
	g := NewWithT(t)

	cluster := newCluster()
	cluster.Spec = clusterv1.ClusterSpec{
		Paused: true,
		ManagedExternalEtcdRef: &corev1.ObjectReference{
			Kind:       "EtcdadmCluster",
			Namespace:  testNamespace,
			Name:       testEtcdadmClusterName,
			APIVersion: etcdv1.GroupVersion.String(),
		},
	}

	etcdadmCluster := &etcdv1.EtcdadmCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      testEtcdadmClusterName,
		},
		Spec: etcdv1.EtcdadmClusterSpec{
			Version: "v3.4.9",
		},
	}

	objects := []runtime.Object{
		cluster,
		etcdadmCluster,
	}
	fakeClient := helpers.NewFakeClientWithScheme(setupScheme(), objects...)

	r := &EtcdadmClusterReconciler{
		Client: fakeClient,
		Log:    log.Log,
	}
	_, err := r.Reconcile(ctrl.Request{NamespacedName: util.ObjectKey(etcdadmCluster)})
	g.Expect(err).NotTo(HaveOccurred())

	machineList := &clusterv1.MachineList{}
	g.Expect(fakeClient.List(context.Background(), machineList, client.InNamespace("test"))).To(Succeed())
	g.Expect(machineList.Items).To(BeEmpty())
}

func TestReconcileInitializeEtcdCluster(t *testing.T) {
	g := NewWithT(t)

	cluster := clusterWithExternalEtcd()

	etcdadmCluster := &etcdv1.EtcdadmCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      testEtcdadmClusterName,
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind:       "Cluster",
					APIVersion: clusterv1.GroupVersion.String(),
					Name:       cluster.Name,
				},
			},
		},
		Spec: etcdv1.EtcdadmClusterSpec{
			Replicas: pointer.Int32Ptr(int32(3)),
			Version:  "v3.4.9",
			InfrastructureTemplate: corev1.ObjectReference{
				Kind:       infraTemplate.GetKind(),
				APIVersion: infraTemplate.GetAPIVersion(),
				Name:       infraTemplate.GetName(),
				Namespace:  testNamespace,
			},
		},
	}

	// no machines or etcdadmConfig objects exist for the etcdadm cluster yet, so it should make a call to initialize the cluster
	// which will create one machine and one etcdadmConfig object
	objects := []runtime.Object{
		cluster,
		etcdadmCluster,
		infraTemplate.DeepCopy(),
	}

	fakeClient := helpers.NewFakeClientWithScheme(setupScheme(), objects...)

	r := &EtcdadmClusterReconciler{
		Client:         fakeClient,
		uncachedClient: fakeClient,
		Log:            log.Log,
	}
	_, err := r.Reconcile(ctrl.Request{NamespacedName: util.ObjectKey(etcdadmCluster)})
	g.Expect(err).NotTo(HaveOccurred())

	machineList := &clusterv1.MachineList{}
	g.Expect(fakeClient.List(context.Background(), machineList, client.InNamespace("test"))).To(Succeed())
	g.Expect(len(machineList.Items)).To(Equal(1))

	etcdadmConfig := &bootstrapv1alpha3.EtcdadmConfigList{}
	g.Expect(fakeClient.List(context.Background(), etcdadmConfig, client.InNamespace("test"))).To(Succeed())
	g.Expect(len(etcdadmConfig.Items)).To(Equal(1))
}

func TestReconcileScaleUpEtcdCluster(t *testing.T) {
	//g := NewWithT(t)
	//
	//cluster := clusterWithExternalEtcd()
	//
	//etcdadmCluster := &etcdv1.EtcdadmCluster{
	//	ObjectMeta: metav1.ObjectMeta{
	//		Namespace: testNamespace,
	//		Name:      testEtcdadmClusterName,
	//		OwnerReferences: []metav1.OwnerReference{
	//			{
	//				Kind:       "Cluster",
	//				APIVersion: clusterv1.GroupVersion.String(),
	//				Name:       cluster.Name,
	//			},
	//		},
	//	},
	//	Spec: etcdv1.EtcdadmClusterSpec{
	//		Replicas: pointer.Int32Ptr(int32(3)),
	//		Version:  "v3.4.9",
	//		InfrastructureTemplate: corev1.ObjectReference{
	//			Kind:       infraTemplate.GetKind(),
	//			APIVersion: infraTemplate.GetAPIVersion(),
	//			Name:       infraTemplate.GetName(),
	//			Namespace:  testNamespace,
	//		},
	//	},
	//	Status: etcdv1.EtcdadmClusterStatus{
	//		Initialized: true,
	//	},
	//}

}

// newCluster return a CAPI cluster object
func newCluster() *clusterv1.Cluster {
	return &clusterv1.Cluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Cluster",
			APIVersion: clusterv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      testClusterName,
		},
	}
}

func clusterWithExternalEtcd() *clusterv1.Cluster {
	return &clusterv1.Cluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Cluster",
			APIVersion: clusterv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      testClusterName,
		},
		Spec: clusterv1.ClusterSpec{
			ManagedExternalEtcdRef: &corev1.ObjectReference{
				Kind:       "EtcdadmCluster",
				Namespace:  testNamespace,
				Name:       testEtcdadmClusterName,
				APIVersion: etcdv1.GroupVersion.String(),
			},
			InfrastructureRef: &corev1.ObjectReference{
				Kind:       "InfrastructureTemplate",
				Namespace:  testNamespace,
				Name:       testInfrastructureTemplateName,
				APIVersion: "infra.io/v1",
			},
		},
	}
}
