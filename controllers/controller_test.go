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
	"time"

	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/cluster-api/util/conditions"

	etcdbootstrapv1 "github.com/aws/etcdadm-bootstrap-provider/api/v1beta1"
	etcdv1 "github.com/aws/etcdadm-controller/api/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	// +kubebuilder:scaffold:imports
)

var ctx = ctrl.SetupSignalHandler()

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controller Suite")
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
	if err := etcdbootstrapv1.AddToScheme(scheme); err != nil {
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

	cluster := newClusterWithExternalEtcd()

	objects := []client.Object{
		cluster,
	}
	fakeClient := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(objects...).Build()

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

	got := r.ClusterToEtcdadmCluster(cluster)

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
			EtcdadmConfigSpec: etcdbootstrapv1.EtcdadmConfigSpec{
				CloudInitConfig: &etcdbootstrapv1.CloudInitConfig{
					Version: "v3.4.9",
				},
			},
		},
	}

	objects := []client.Object{
		etcdadmCluster,
	}
	fakeClient := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(objects...).Build()

	r := &EtcdadmClusterReconciler{
		Client: fakeClient,
		Log:    log.Log,
	}

	result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: util.ObjectKey(etcdadmCluster)})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(result).To(Equal(ctrl.Result{}))

	machineList := &clusterv1.MachineList{}
	g.Expect(fakeClient.List(context.Background(), machineList, client.InNamespace("test"))).To(Succeed())
	g.Expect(machineList.Items).To(BeEmpty())
}

func TestReconcilePaused(t *testing.T) {
	g := NewWithT(t)

	cluster := newClusterWithExternalEtcd()
	cluster.Spec.Paused = true

	etcdadmCluster := newEtcdadmCluster(cluster)

	objects := []client.Object{
		cluster,
		etcdadmCluster,
	}
	fakeClient := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(objects...).Build()

	r := &EtcdadmClusterReconciler{
		Client: fakeClient,
		Log:    log.Log,
	}
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: util.ObjectKey(etcdadmCluster)})
	g.Expect(err).NotTo(HaveOccurred())

	machineList := &clusterv1.MachineList{}
	g.Expect(fakeClient.List(context.Background(), machineList, client.InNamespace("test"))).To(Succeed())
	g.Expect(machineList.Items).To(BeEmpty())

	// Test: etcdcluster is paused and cluster is not
	cluster.Spec.Paused = false
	etcdadmCluster.ObjectMeta.Annotations = map[string]string{}
	etcdadmCluster.ObjectMeta.Annotations[clusterv1.PausedAnnotation] = "paused"
	_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: util.ObjectKey(etcdadmCluster)})
	g.Expect(err).NotTo(HaveOccurred())
}

// If cluster infrastructure is not ready, reconcile won't proceed and will requeue etcdadmCluster to be processed after 5 sec
func TestReconcileClusterInfrastructureNotReady(t *testing.T) {
	g := NewWithT(t)

	cluster := newClusterWithExternalEtcd()
	cluster.Status.InfrastructureReady = false

	etcdadmCluster := newEtcdadmCluster(cluster)
	etcdadmCluster.ObjectMeta.Finalizers = []string{}

	// no machines or etcdadmConfig objects exist for the etcdadm cluster yet, so it should make a call to initialize the cluster
	// which will create one machine and one etcdadmConfig object
	objects := []client.Object{
		cluster,
		etcdadmCluster,
		infraTemplate.DeepCopy(),
	}

	fakeClient := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(objects...).Build()

	r := &EtcdadmClusterReconciler{
		Client:         fakeClient,
		uncachedClient: fakeClient,
		Log:            log.Log,
	}
	result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: util.ObjectKey(etcdadmCluster)})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(result).To(Equal(ctrl.Result{Requeue: false, RequeueAfter: 5 * time.Second}))
}

func TestReconcileNoFinalizer(t *testing.T) {
	g := NewWithT(t)

	cluster := newClusterWithExternalEtcd()

	etcdadmCluster := newEtcdadmCluster(cluster)
	etcdadmCluster.ObjectMeta.Finalizers = []string{}

	// no machines or etcdadmConfig objects exist for the etcdadm cluster yet, so it should make a call to initialize the cluster
	// which will create one machine and one etcdadmConfig object
	objects := []client.Object{
		cluster,
		etcdadmCluster,
		infraTemplate.DeepCopy(),
	}

	fakeClient := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(objects...).Build()

	r := &EtcdadmClusterReconciler{
		Client:         fakeClient,
		uncachedClient: fakeClient,
		Log:            log.Log,
	}
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: util.ObjectKey(etcdadmCluster)})
	g.Expect(err).NotTo(HaveOccurred())

	updatedEtcdadmCluster := etcdv1.EtcdadmCluster{}
	g.Expect(fakeClient.Get(ctx, util.ObjectKey(etcdadmCluster), &updatedEtcdadmCluster)).To(Succeed())
	g.Expect(len(updatedEtcdadmCluster.Finalizers)).ToNot(BeZero())
}

func TestReconcileInitializeEtcdCluster(t *testing.T) {
	g := NewWithT(t)

	cluster := newClusterWithExternalEtcd()
	etcdadmCluster := newEtcdadmCluster(cluster)

	// no machines or etcdadmConfig objects exist for the etcdadm cluster yet, so it should make a call to initialize the cluster
	// which will create one machine and one etcdadmConfig object
	objects := []client.Object{
		cluster,
		etcdadmCluster,
		infraTemplate.DeepCopy(),
	}

	fakeClient := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(objects...).Build()

	r := &EtcdadmClusterReconciler{
		Client:         fakeClient,
		uncachedClient: fakeClient,
		Log:            log.Log,
	}
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: util.ObjectKey(etcdadmCluster)})
	g.Expect(err).NotTo(HaveOccurred())

	machineList := &clusterv1.MachineList{}
	g.Expect(fakeClient.List(context.Background(), machineList, client.InNamespace("test"))).To(Succeed())
	g.Expect(len(machineList.Items)).To(Equal(1))

	etcdadmConfig := &etcdbootstrapv1.EtcdadmConfigList{}
	g.Expect(fakeClient.List(context.Background(), etcdadmConfig, client.InNamespace("test"))).To(Succeed())
	g.Expect(len(etcdadmConfig.Items)).To(Equal(1))

	updatedEtcdadmCluster := &etcdv1.EtcdadmCluster{}
	g.Expect(fakeClient.Get(ctx, util.ObjectKey(etcdadmCluster), updatedEtcdadmCluster)).To(Succeed())
	g.Expect(conditions.IsFalse(updatedEtcdadmCluster, etcdv1.InitializedCondition)).To(BeTrue())
	g.Expect(conditions.IsFalse(updatedEtcdadmCluster, etcdv1.EtcdEndpointsAvailable)).To(BeTrue())
}

func TestReconcile_EtcdClusterNotInitialized(t *testing.T) {
	g := NewWithT(t)

	cluster := newClusterWithExternalEtcd()
	etcdadmCluster := newEtcdadmCluster(cluster)

	// CAPI machine controller has not yet created the first etcd Machine, so it has not yet set Initialized to true
	etcdadmCluster.Status.Initialized = false
	conditions.MarkFalse(etcdadmCluster, etcdv1.InitializedCondition, etcdv1.WaitingForEtcdadmInitReason, clusterv1.ConditionSeverityInfo, "")
	machine := newEtcdMachine(etcdadmCluster, cluster)

	objects := []client.Object{
		cluster,
		etcdadmCluster,
		infraTemplate.DeepCopy(),
		machine,
	}
	fakeClient := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(objects...).Build()

	r := &EtcdadmClusterReconciler{
		Client:         fakeClient,
		uncachedClient: fakeClient,
		Log:            log.Log,
	}
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: util.ObjectKey(etcdadmCluster)})
	g.Expect(err).NotTo(HaveOccurred())

	updatedEtcdadmCluster := &etcdv1.EtcdadmCluster{}
	g.Expect(fakeClient.Get(ctx, util.ObjectKey(etcdadmCluster), updatedEtcdadmCluster)).To(Succeed())
	g.Expect(conditions.IsTrue(updatedEtcdadmCluster, etcdv1.InitializedCondition)).To(BeFalse())
}

func TestReconcile_EtcdClusterIsInitialized(t *testing.T) {
	g := NewWithT(t)

	cluster := newClusterWithExternalEtcd()
	etcdadmCluster := newEtcdadmCluster(cluster)

	// CAPI machine controller has set status.Initialized to true, after the first etcd Machine is created, and after creating the Secret containing etcd init address
	etcdadmCluster.Status.Initialized = true
	// the etcdadm controller does not know yet that CAPI machine controller has set status.Initialized to true; InitializedCondition is still false
	conditions.MarkFalse(etcdadmCluster, etcdv1.InitializedCondition, etcdv1.WaitingForEtcdadmInitReason, clusterv1.ConditionSeverityInfo, "")
	machine := newEtcdMachine(etcdadmCluster, cluster)

	objects := []client.Object{
		cluster,
		etcdadmCluster,
		infraTemplate.DeepCopy(),
		machine,
	}
	fakeClient := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(objects...).Build()

	r := &EtcdadmClusterReconciler{
		Client:         fakeClient,
		uncachedClient: fakeClient,
		Log:            log.Log,
	}
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: util.ObjectKey(etcdadmCluster)})
	g.Expect(err).NotTo(HaveOccurred())

	updatedEtcdadmCluster := &etcdv1.EtcdadmCluster{}
	g.Expect(fakeClient.Get(ctx, util.ObjectKey(etcdadmCluster), updatedEtcdadmCluster)).To(Succeed())
	g.Expect(conditions.IsTrue(updatedEtcdadmCluster, etcdv1.InitializedCondition)).To(BeTrue())
}

func TestReconcileScaleUpEtcdCluster(t *testing.T) {
	g := NewWithT(t)

	cluster := newClusterWithExternalEtcd()
	etcdadmCluster := newEtcdadmCluster(cluster)

	// CAPI machine controller has set status.Initialized to true, after the first etcd Machine is created, and after creating the Secret containing etcd init address
	etcdadmCluster.Status.Initialized = true
	// etcdadm controller has also registered that the status.Initialized field is true, so it has set InitializedCondition to true
	conditions.MarkTrue(etcdadmCluster, etcdv1.InitializedCondition)
	machine := newEtcdMachine(etcdadmCluster, cluster)

	objects := []client.Object{
		cluster,
		etcdadmCluster,
		infraTemplate.DeepCopy(),
		machine,
	}
	fakeClient := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(objects...).Build()

	r := &EtcdadmClusterReconciler{
		Client:         fakeClient,
		uncachedClient: fakeClient,
		Log:            log.Log,
	}
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: util.ObjectKey(etcdadmCluster)})
	g.Expect(err).NotTo(HaveOccurred())

	machineList := &clusterv1.MachineList{}
	g.Expect(fakeClient.List(context.Background(), machineList, client.InNamespace("test"))).To(Succeed())
	g.Expect(len(machineList.Items)).To(Equal(2))
}

// newClusterWithExternalEtcd return a CAPI cluster object with managed external etcd ref
func newClusterWithExternalEtcd() *clusterv1.Cluster {
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
		Status: clusterv1.ClusterStatus{
			InfrastructureReady: true,
		},
	}
}

func newEtcdadmCluster(cluster *clusterv1.Cluster) *etcdv1.EtcdadmCluster {
	return &etcdv1.EtcdadmCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      testEtcdadmClusterName,
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind:       "Cluster",
					APIVersion: clusterv1.GroupVersion.String(),
					Name:       cluster.Name,
					UID:        cluster.GetUID(),
				},
			},
			Finalizers: []string{etcdv1.EtcdadmClusterFinalizer},
		},
		Spec: etcdv1.EtcdadmClusterSpec{
			EtcdadmConfigSpec: etcdbootstrapv1.EtcdadmConfigSpec{
				CloudInitConfig: &etcdbootstrapv1.CloudInitConfig{
					Version: "v3.4.9",
				},
			},
			Replicas: pointer.Int32Ptr(int32(3)),
			InfrastructureTemplate: corev1.ObjectReference{
				Kind:       infraTemplate.GetKind(),
				APIVersion: infraTemplate.GetAPIVersion(),
				Name:       infraTemplate.GetName(),
				Namespace:  testNamespace,
			},
		},
	}
}

func newEtcdMachine(etcdadmCluster *etcdv1.EtcdadmCluster, cluster *clusterv1.Cluster) *clusterv1.Machine {
	return &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.SimpleNameGenerator.GenerateName(etcdadmCluster.Name + "-"),
			Namespace: etcdadmCluster.Namespace,
			Labels:    EtcdLabelsForCluster(cluster.Name, etcdadmCluster.Name),
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(etcdadmCluster, etcdv1.GroupVersion.WithKind("EtcdadmCluster")),
			},
		},
		Spec: clusterv1.MachineSpec{
			ClusterName: cluster.Name,
			InfrastructureRef: corev1.ObjectReference{
				Kind:       infraTemplate.GetKind(),
				APIVersion: infraTemplate.GetAPIVersion(),
				Name:       infraTemplate.GetName(),
				Namespace:  infraTemplate.GetNamespace(),
			},
		},
	}
}
