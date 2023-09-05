package controllers

import (
	"context"
	"net/http"
	"testing"

	"github.com/aws/etcdadm-controller/controllers/mocks"
	"github.com/golang/mock/gomock"
	. "github.com/onsi/gomega"
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

	etcdTest := newEtcdadmClusterTest()
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

	mockRt.EXPECT().RoundTrip(gomock.Any()).Return(healthyEtcdResponse, nil).Times(3)

	etcdadmClusterMapper := make(map[types.UID]etcdadmClusterMemberHealthConfig)
	r.startHealthCheck(context.Background(), etcdadmClusterMapper)
}


func isPortOpenMock(_ context.Context, _ string) bool {
	return true
}
