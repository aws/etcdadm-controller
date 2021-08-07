module github.com/mrajashree/etcdadm-controller

go 1.13

require (
	github.com/go-logr/logr v0.1.0
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/mrajashree/etcdadm-bootstrap-provider v0.1.1-0.20210807004059-42797f8a87fd
	github.com/onsi/ginkgo v1.12.1
	github.com/onsi/gomega v1.10.1
	github.com/pkg/errors v0.9.1
	go.etcd.io/etcd/api/v3 v3.5.0-beta.4
	go.etcd.io/etcd/client/v3 v3.5.0-beta.4
	google.golang.org/grpc v1.38.0 // indirect
	k8s.io/api v0.17.9
	k8s.io/apimachinery v0.17.9
	k8s.io/apiserver v0.17.9
	k8s.io/client-go v0.17.9
	k8s.io/utils v0.0.0-20210305010621-2afb4311ab10
	sigs.k8s.io/cluster-api v0.3.11-0.20210329151847-96ab9172b7c1
	sigs.k8s.io/controller-runtime v0.5.14
	sigs.k8s.io/etcdadm v0.1.3
)

replace sigs.k8s.io/cluster-api => github.com/mrajashree/cluster-api v0.3.15-0.20210807001639-ad3c6b449740
