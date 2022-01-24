module github.com/mrajashree/etcdadm-controller

go 1.16

require (
	github.com/go-logr/logr v0.4.0
	github.com/hashicorp/go-multierror v1.1.1
	github.com/mrajashree/etcdadm-bootstrap-provider v1.0.0-rc4
	github.com/onsi/ginkgo v1.16.4
	github.com/onsi/gomega v1.16.0
	github.com/pkg/errors v0.9.1
	go.etcd.io/etcd/api/v3 v3.5.0
	go.etcd.io/etcd/client/v3 v3.5.0
	k8s.io/api v0.22.2
	k8s.io/apimachinery v0.22.2
	k8s.io/apiserver v0.22.2
	k8s.io/client-go v0.22.2
	k8s.io/utils v0.0.0-20210930125809-cb0fa318a74b
	sigs.k8s.io/cluster-api v1.0.2
	sigs.k8s.io/controller-runtime v0.10.3
	sigs.k8s.io/etcdadm v0.1.5
)

replace sigs.k8s.io/cluster-api => github.com/mrajashree/cluster-api v1.0.2-custom
