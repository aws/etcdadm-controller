package controllers

import (
	"context"
	"net/http"
	"testing"

	"github.com/aws/etcdadm-controller/controllers/mocks"
	"github.com/golang/mock/gomock"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func TestStartHealthCheckLoop(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockEtcd := mocks.NewMockEtcdClient(ctrl)
	mockRt := mocks.NewMockRoundTripper(ctrl)

	etcdTest := newEtcdadmClusterTest()
	etcdTest.buildClusterWithExternalEtcd()
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

	mockRt.EXPECT().RoundTrip(gomock.Any()).Return(getHealthyEtcdResponse(), nil).Times(3)

	etcdadmClusterMapper := make(map[types.UID]etcdadmClusterMemberHealthConfig)
	r.startHealthCheck(context.Background(), etcdadmClusterMapper)
}

func TestStartHealthCheckLoopWithNoRetries(t *testing.T) {
	g := NewWithT(t)
	ctrl := gomock.NewController(t)
	mockEtcd := mocks.NewMockEtcdClient(ctrl)
	mockRt := mocks.NewMockRoundTripper(ctrl)

	etcdTest := newEtcdadmClusterTest()
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

	etcdTest := newEtcdadmClusterTest()
	etcdTest.buildClusterWithExternalEtcd().withHealthCheckRetries(3)
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

	mockRt.EXPECT().RoundTrip(gomock.Any()).Return(nil, errors.New("error")).Times(9)
	mockEtcd.EXPECT().MemberList(gomock.Any()).Return(etcdTest.getMemberListResponse(), nil).Times(3)
	mockEtcd.EXPECT().Close().Times(3)

	r.startHealthCheck(context.Background(), etcdadmClusterMapper)
	g.Expect(etcdTest.getDeletedMachines(fakeKubernetesClient)).To(BeEmpty())

	r.startHealthCheck(context.Background(), etcdadmClusterMapper)
	g.Expect(etcdTest.getDeletedMachines(fakeKubernetesClient)).To(BeEmpty())

	r.startHealthCheck(context.Background(), etcdadmClusterMapper)
	g.Expect(etcdTest.getDeletedMachines(fakeKubernetesClient)).To(HaveLen(3))
}

type RoundTripperFunc func(*http.Request) (*http.Response, error)

func (fn RoundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func isPortOpenMock(_ context.Context, _ string) bool {
	return true
}
