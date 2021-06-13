module github.com/mrajashree/etcdadm-controller

go 1.16

require (
	github.com/go-logr/logr v0.4.0
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/mrajashree/etcdadm-bootstrap-provider v0.0.1-0.20210608213731-e1f9a703936e
	github.com/onsi/ginkgo v1.15.2
	github.com/onsi/gomega v1.11.0
	github.com/pkg/errors v0.9.1
	go.etcd.io/etcd/api/v3 v3.5.0-beta.4
	go.etcd.io/etcd/client/v3 v3.5.0-beta.4
	go.uber.org/zap v1.17.0 // indirect
	google.golang.org/grpc v1.38.0 // indirect
	k8s.io/api v0.21.0-beta.1
	k8s.io/apimachinery v0.21.0-beta.1
	k8s.io/apiserver v0.21.0-beta.1
	k8s.io/client-go v0.21.0-beta.1
	sigs.k8s.io/cluster-api v0.3.11-0.20210329151847-96ab9172b7c1
	sigs.k8s.io/controller-runtime v0.9.0-alpha.1
	sigs.k8s.io/etcdadm v0.1.3
)

replace sigs.k8s.io/cluster-api => github.com/mrajashree/cluster-api v0.3.11-0.20210426220346-18c42c47d024
