package controllers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/aws/etcdadm-controller/controllers/mocks"
	"github.com/golang/mock/gomock"
	"k8s.io/apimachinery/pkg/types"

	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func TestStartHealthCheckLoop(t *testing.T) {
	_ = NewWithT(t)

	ctrl := gomock.NewController(t)
	mockEtcd := mocks.NewMockEtcdClient(ctrl)
	mockRt := mocks.NewMockRoundTripper(ctrl)

	etcdTest := newEtcdadmClusterTest(3)
	etcdTest.buildClusterWithExternalEtcd()
	etcdTest.etcdadmCluster.Status.CreationComplete = true

	fakeKubernetesClient := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(etcdTest.gatherObjects()...).Build()

	etcdEtcdClient := func(ctx context.Context, cluster *clusterv1.Cluster, endpoints string) (EtcdClient, error) {
		return mockEtcd, nil
	}

	r := &EtcdadmClusterReconciler{
		Client:         fakeKubernetesClient,
		uncachedClient: fakeKubernetesClient,
		Log:            log.Log,
		GetEtcdClient:  etcdEtcdClient,
	}
	mockHttpClient := &http.Client{
		Transport: mockRt,
	}

	r.etcdHealthCheckConfig.clusterToHttpClient.Store(etcdTest.cluster.UID, mockHttpClient)
	r.SetIsPortOpen(isPortOpenMock)

	mockRt.EXPECT().RoundTrip(gomock.Any()).Return(getHealthyEtcdResponse(), nil).Times(3)

	etcdadmClusterMapper := make(map[types.UID]etcdadmClusterMemberHealthConfig)
	r.startHealthCheck(context.Background(), etcdadmClusterMapper)
}

func TestStartHealthCheckLoopWithNoRetries(t *testing.T) {
	g := NewWithT(t)
	ctrl := gomock.NewController(t)
	mockEtcd := mocks.NewMockEtcdClient(ctrl)
	mockRt := mocks.NewMockRoundTripper(ctrl)

	etcdTest := newEtcdadmClusterTest(3)
	etcdTest.buildClusterWithExternalEtcd().withHealthCheckRetries(0)
	etcdTest.etcdadmCluster.Status.CreationComplete = true

	fakeKubernetesClient := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(etcdTest.gatherObjects()...).Build()

	mockEtcdClient := func(ctx context.Context, cluster *clusterv1.Cluster, endpoints string) (EtcdClient, error) {
		return mockEtcd, nil
	}

	r := &EtcdadmClusterReconciler{
		Client:         fakeKubernetesClient,
		uncachedClient: fakeKubernetesClient,
		Log:            log.Log,
		GetEtcdClient:  mockEtcdClient,
	}
	mockHttpClient := &http.Client{
		Transport: mockRt,
	}

	r.etcdHealthCheckConfig.clusterToHttpClient.Store(etcdTest.cluster.UID, mockHttpClient)
	r.SetIsPortOpen(isPortOpenMock)

	mockRt.EXPECT().RoundTrip(gomock.Any()).Return(nil, errors.New("error")).Times(3)

	etcdadmClusterMapper := make(map[types.UID]etcdadmClusterMemberHealthConfig)
	r.startHealthCheck(context.Background(), etcdadmClusterMapper)

	g.Expect(etcdadmClusterMapper).To(BeEmpty())
}

func TestStartHealthCheckLoopWithCustomRetries(t *testing.T) {
	g := NewWithT(t)
	ctrl := gomock.NewController(t)
	mockEtcd := mocks.NewMockEtcdClient(ctrl)
	mockRt := mocks.NewMockRoundTripper(ctrl)
	etcdadmClusterMapper := make(map[types.UID]etcdadmClusterMemberHealthConfig)

	etcdTest := newEtcdadmClusterTest(3)
	etcdTest.buildClusterWithExternalEtcd().withHealthCheckRetries(3)
	etcdTest.etcdadmCluster.Status.CreationComplete = true

	ip := etcdTest.machines[0].Status.Addresses[0].Address

	fakeKubernetesClient := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(etcdTest.gatherObjects()...).Build()
	mockEtcdClient := func(ctx context.Context, cluster *clusterv1.Cluster, endpoints string) (EtcdClient, error) {
		return mockEtcd, nil
	}

	r := &EtcdadmClusterReconciler{
		Client:         fakeKubernetesClient,
		uncachedClient: fakeKubernetesClient,
		Log:            log.Log,
		GetEtcdClient:  mockEtcdClient,
	}
	mockHttpClient := &http.Client{
		Transport: RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.Host, ip) {
				return nil, fmt.Errorf("Error")
			}
			return getHealthyEtcdResponse(), nil
		}),
	}

	r.etcdHealthCheckConfig.clusterToHttpClient.Store(etcdTest.cluster.UID, mockHttpClient)
	r.SetIsPortOpen(isPortOpenMock)

	mockRt.EXPECT().RoundTrip(gomock.Any()).Return(nil, errors.New("error")).Times(9)
	mockEtcd.EXPECT().MemberList(gomock.Any()).Return(etcdTest.getMemberListResponse(), nil).Times(3)
	mockEtcd.EXPECT().MemberRemove(gomock.Any(), gomock.Any()).Return(etcdTest.getMemberRemoveResponse(), nil).Times(1)
	mockEtcd.EXPECT().Close().Times(3)

	r.startHealthCheck(context.Background(), etcdadmClusterMapper)
	g.Expect(etcdTest.getDeletedMachines(fakeKubernetesClient)).To(BeEmpty())

	r.startHealthCheck(context.Background(), etcdadmClusterMapper)
	g.Expect(etcdTest.getDeletedMachines(fakeKubernetesClient)).To(BeEmpty())

	r.startHealthCheck(context.Background(), etcdadmClusterMapper)
	g.Expect(etcdTest.getDeletedMachines(fakeKubernetesClient)).To(HaveLen(1))
}

func TestReconcilePeriodicHealthCheckMachineToBeDeletedNowHealthy(t *testing.T) {
	g := NewWithT(t)

	ctrl := gomock.NewController(t)
	mockEtcd := mocks.NewMockEtcdClient(ctrl)
	mockRt := mocks.NewMockRoundTripper(ctrl)

	etcdadmCluster := newEtcdadmClusterTest(1)
	etcdadmCluster.buildClusterWithExternalEtcd()
	etcdadmCluster.etcdadmCluster.Status.CreationComplete = true

	fakeKubernetesClient := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(etcdadmCluster.gatherObjects()...).Build()

	etcdEtcdClient := func(ctx context.Context, cluster *clusterv1.Cluster, endpoints string) (EtcdClient, error) {
		return mockEtcd, nil
	}

	r := &EtcdadmClusterReconciler{
		Client:         fakeKubernetesClient,
		uncachedClient: fakeKubernetesClient,
		Log:            log.Log,
		GetEtcdClient:  etcdEtcdClient,
	}
	mockHttpClient := &http.Client{
		Transport: mockRt,
	}

	r.etcdHealthCheckConfig.clusterToHttpClient.Store(etcdadmCluster.cluster.UID, mockHttpClient)
	r.SetIsPortOpen(isPortOpenMock)

	etcdadmClusterMapper := make(map[types.UID]etcdadmClusterMemberHealthConfig)

	mockRt.EXPECT().RoundTrip(gomock.Any()).Return(nil, errors.New("error")).Times(5)
	mockEtcd.EXPECT().MemberList(gomock.Any()).Return(etcdadmCluster.getMemberListResponse(), nil).Times(5)
	mockEtcd.EXPECT().Close().Times(5)

	for i := 0; i < 5; i++ {
		r.startHealthCheck(context.Background(), etcdadmClusterMapper)
	}

	g.Expect(etcdadmClusterMapper[etcdadmCluster.etcdadmCluster.UID].unhealthyMembersToRemove).To(HaveLen(1))

	mockRt.EXPECT().RoundTrip(gomock.Any()).Return(getHealthyEtcdResponse(), nil)
	r.startHealthCheck(context.Background(), etcdadmClusterMapper)

	g.Expect(etcdadmClusterMapper[etcdadmCluster.etcdadmCluster.UID].unhealthyMembersToRemove).To(HaveLen(0))
}

func TestQuorumNotPreserved(t *testing.T) {
	g := NewWithT(t)

	ctrl := gomock.NewController(t)
	mockEtcd := mocks.NewMockEtcdClient(ctrl)
	mockRt := mocks.NewMockRoundTripper(ctrl)

	etcdTest := newEtcdadmClusterTest(3)
	etcdTest.buildClusterWithExternalEtcd()
	etcdTest.etcdadmCluster.Status.CreationComplete = true

	fakeKubernetesClient := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(etcdTest.gatherObjects()...).Build()

	etcdEtcdClient := func(ctx context.Context, cluster *clusterv1.Cluster, endpoints string) (EtcdClient, error) {
		return mockEtcd, nil
	}

	r := &EtcdadmClusterReconciler{
		Client:         fakeKubernetesClient,
		uncachedClient: fakeKubernetesClient,
		Log:            log.Log,
		GetEtcdClient:  etcdEtcdClient,
	}
	mockHttpClient := &http.Client{
		Transport: mockRt,
	}

	r.etcdHealthCheckConfig.clusterToHttpClient.Store(etcdTest.cluster.UID, mockHttpClient)
	r.SetIsPortOpen(isPortOpenMock)

	etcdadmClusterMapper := make(map[types.UID]etcdadmClusterMemberHealthConfig)

	mockRt.EXPECT().RoundTrip(gomock.Any()).Return(nil, errors.New("error")).Times(15)
	mockEtcd.EXPECT().MemberList(gomock.Any()).Return(etcdTest.getMemberListResponse(), nil).Times(5)
	mockEtcd.EXPECT().Close().Times(5)

	for i := 0; i < 5; i++ {
		r.startHealthCheck(context.Background(), etcdadmClusterMapper)
	}

	unhealthyList := len(etcdadmClusterMapper[etcdTest.etcdadmCluster.UID].unhealthyMembersToRemove)
	g.Expect(unhealthyList).To(Equal(3))

	mockRt.EXPECT().RoundTrip(gomock.Any()).Return(nil, errors.New("error")).Times(9)
	mockEtcd.EXPECT().MemberList(gomock.Any()).Times(3)
	mockEtcd.EXPECT().Close().Times(3)
	for i := 0; i < 3; i++ {
		r.startHealthCheck(context.Background(), etcdadmClusterMapper)
	}

	machineList := &clusterv1.MachineList{}
	g.Expect(fakeKubernetesClient.List(context.Background(), machineList)).To(Succeed())
	for _, m := range machineList.Items {
		g.Expect(m.DeletionTimestamp.IsZero()).To(BeTrue())
	}
	g.Expect(etcdadmClusterMapper[etcdTest.etcdadmCluster.UID].unhealthyMembersToRemove).To(HaveLen(3))
}

func TestQuorumPreserved(t *testing.T) {
	g := NewWithT(t)

	ctrl := gomock.NewController(t)
	mockEtcd := mocks.NewMockEtcdClient(ctrl)

	etcdTest := newEtcdadmClusterTest(5)
	etcdTest.buildClusterWithExternalEtcd()
	etcdTest.etcdadmCluster.Status.CreationComplete = true

	ip1 := etcdTest.machines[0].Status.Addresses[0].Address
	ip2 := etcdTest.machines[1].Status.Addresses[0].Address

	fakeKubernetesClient := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(etcdTest.gatherObjects()...).Build()

	etcdEtcdClient := func(ctx context.Context, cluster *clusterv1.Cluster, endpoints string) (EtcdClient, error) {
		return mockEtcd, nil
	}

	r := &EtcdadmClusterReconciler{
		Client:         fakeKubernetesClient,
		uncachedClient: fakeKubernetesClient,
		Log:            log.Log,
		GetEtcdClient:  etcdEtcdClient,
	}
	mockHttpClient := &http.Client{
		Transport: RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.Host, ip1) || strings.Contains(req.Host, ip2) {
				return nil, fmt.Errorf("Error")
			}
			return getHealthyEtcdResponse(), nil
		}),
	}

	r.etcdHealthCheckConfig.clusterToHttpClient.Store(etcdTest.cluster.UID, mockHttpClient)
	r.SetIsPortOpen(isPortOpenMock)

	etcdadmClusterMapper := make(map[types.UID]etcdadmClusterMemberHealthConfig)

	mockEtcd.EXPECT().MemberList(gomock.Any()).Return(etcdTest.getMemberListResponse(), nil).Times(5)
	mockEtcd.EXPECT().MemberRemove(gomock.Any(), gomock.Any()).Return(etcdTest.getMemberRemoveResponse(), nil).Times(1)
	mockEtcd.EXPECT().Close().Times(5)
	for i := 0; i < 5; i++ {
		r.startHealthCheck(context.Background(), etcdadmClusterMapper)
	}

	g.Expect(etcdadmClusterMapper[etcdTest.etcdadmCluster.UID].unhealthyMembersFrequency).To(HaveLen(2))
	g.Expect(etcdTest.getDeletedMachines(fakeKubernetesClient)).To(HaveLen(1))
}

type RoundTripperFunc func(*http.Request) (*http.Response, error)

func (fn RoundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func isPortOpenMock(_ context.Context, _ string) bool {
	return true
}
