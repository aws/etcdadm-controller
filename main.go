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

package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	etcdbp "github.com/aws/etcdadm-bootstrap-provider/api/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	etcdclusterv1alpha3 "github.com/aws/etcdadm-controller/api/v1alpha3"
	etcdclusterv1beta1 "github.com/aws/etcdadm-controller/api/v1beta1"
	"github.com/aws/etcdadm-controller/controllers"
	// +kubebuilder:scaffold:imports
)

var (
	scheme         = runtime.NewScheme()
	setupLog       = ctrl.Log.WithName("setup")
	watchNamespace string
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)

	_ = clusterv1.AddToScheme(scheme)
	_ = etcdbp.AddToScheme(scheme)
	_ = etcdclusterv1alpha3.AddToScheme(scheme)
	_ = etcdclusterv1beta1.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var maxConcurrentReconciles int
	var healthcheckInterval int
	flag.StringVar(&metricsAddr, "metrics-addr", "localhost:8080", "The address the metric endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&watchNamespace, "namespace", "",
		"Namespace that the controller watches to reconcile etcdadmCluster objects. If unspecified, the controller watches for objects across all namespaces.")
	flag.IntVar(&maxConcurrentReconciles, "max-concurrent-reconciles", 10, "The maximum number of concurrent etcdadm-controller reconciles.")
	flag.IntVar(&healthcheckInterval, "healthcheck-interval", 30, "The time interval between each healthcheck loop in seconds.")
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: metricsAddr,
		Port:               9443,
		LeaderElection:     enableLeaderElection,
		LeaderElectionID:   "cc88008e.cluster.x-k8s.io",
		Namespace:          watchNamespace,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Setup the context that's going to be used in controllers and for the manager.
	ctx, stopCh := setupSignalHandler()
	etcdadmReconciler := &controllers.EtcdadmClusterReconciler{
		Client:                  mgr.GetClient(),
		Log:                     ctrl.Log.WithName("controllers").WithName("EtcdadmCluster"),
		Scheme:                  mgr.GetScheme(),
		MaxConcurrentReconciles: maxConcurrentReconciles,
		HealthCheckInterval:     time.Second * time.Duration(healthcheckInterval),
	}
	if err = (etcdadmReconciler).SetupWithManager(ctx, mgr, stopCh); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "EtcdadmCluster")
		os.Exit(1)
	}
	if err = (&etcdclusterv1beta1.EtcdadmCluster{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "EtcdadmCluster")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

var onlyOneSignalHandler = make(chan struct{})
var shutdownSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}

/*
	Controller runtime 0.5.4 returns a stop channel and 0.7.0 onwards returns a context that can be passed down to SetupWithManager and reconcilers

Because cluster-api v0.3.x uses controller-runtime 0.5.4 version, etcdadm-controller cannot switch to a higher controller-runtime due to version mismatch errors
So this function setupSignalHandler is a modified version of controller-runtime's SetupSignalHandler that returns both, a stop channel and a context that
is cancelled when this controller exits
*/
func setupSignalHandler() (context.Context, <-chan struct{}) {
	close(onlyOneSignalHandler) // panics when called twice

	ctx, cancel := context.WithCancel(context.Background())
	stop := make(chan struct{})
	c := make(chan os.Signal, 2)
	signal.Notify(c, shutdownSignals...)
	go func() {
		<-c
		cancel()
		close(stop)
		<-c
		os.Exit(1) // second signal. Exit directly.
	}()

	return ctx, stop
}
