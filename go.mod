module github.com/mrajashree/etcdadm-controller

go 1.16

require (
	github.com/go-logr/logr v0.4.0
	github.com/mrajashree/etcdadm-bootstrap-provider v0.0.0
	github.com/onsi/ginkgo v1.15.2
	github.com/onsi/gomega v1.11.0
	github.com/pkg/errors v0.9.1
	k8s.io/api v0.21.0-beta.1
	k8s.io/apimachinery v0.21.0-beta.1
	k8s.io/apiserver v0.21.0-beta.1
	k8s.io/client-go v0.21.0-beta.1
	sigs.k8s.io/cluster-api v0.3.11-0.20210329151847-96ab9172b7c1
	sigs.k8s.io/controller-runtime v0.9.0-alpha.1
	sigs.k8s.io/etcdadm v0.1.3
)

replace sigs.k8s.io/cluster-api => github.com/mrajashree/cluster-api v0.3.11-0.20210423184405-86ec2ba62332
