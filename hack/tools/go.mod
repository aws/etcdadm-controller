module github.com/aws/etcdadm-controller/hack/tools

go 1.16

require (
	github.com/blang/semver v3.5.1+incompatible
	github.com/drone/envsubst/v2 v2.0.0-20210305151453-490366e43a3c
	github.com/go-bindata/go-bindata v3.1.2+incompatible
	github.com/golangci/golangci-lint v1.27.0
	github.com/joelanford/go-apidiff v0.0.0-20191206194835-106bcff5f060
	github.com/onsi/ginkgo v1.16.4
	github.com/raviqqe/liche v0.0.0-20200229003944-f57a5d1c5be4
	github.com/sergi/go-diff v1.1.0 // indirect
	github.com/valyala/fasthttp v1.34.0 // indirect
	golang.org/x/tools v0.1.5
	gopkg.in/yaml.v3 v3.0.0 // indirect
	k8s.io/code-generator v0.22.2
	sigs.k8s.io/controller-tools v0.7.0
	sigs.k8s.io/kubebuilder/docs/book/utils v0.0.0-20210702145813-742983631190
)

replace (
	github.com/prometheus/client_golang => github.com/prometheus/client_golang v1.11.1
	golang.org/x/crypto/ssh => golang.org/x/crypto/ssh v0.0.0-20220314234659-1baeb1ce4c0b
	golang.org/x/net/http => golang.org/x/net/http v0.0.0-20220906165146-f3363e06e74c
)
