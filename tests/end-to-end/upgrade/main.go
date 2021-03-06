package main

import (
	"flag"
	"fmt"

	"k8s.io/client-go/dynamic"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	dex "github.com/kyma-project/kyma/tests/end-to-end/backup-restore-test/utils/fetch-dex-token"

	"github.com/kyma-project/kyma/components/installer/pkg/overrides"
	namespaceController "github.com/kyma-project/kyma/tests/end-to-end/upgrade/pkg/tests/namespace-controller"

	sc "github.com/kubernetes-incubator/service-catalog/pkg/client/clientset_generated/clientset"

	apiController "github.com/kyma-project/kyma/tests/end-to-end/upgrade/pkg/tests/api-controller"
	"github.com/sirupsen/logrus"
	"github.com/vrischmann/envconfig"
	k8sClientSet "k8s.io/client-go/kubernetes"
	restClient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	kubeless "github.com/kubeless/kubeless/pkg/client/clientset/versioned"
	mfClient "github.com/kyma-project/kyma/common/microfrontend-client/pkg/client/clientset/versioned"
	gateway "github.com/kyma-project/kyma/components/api-controller/pkg/clients/gateway.kyma-project.io/clientset/versioned"
	kyma "github.com/kyma-project/kyma/components/api-controller/pkg/clients/gateway.kyma-project.io/clientset/versioned"
	ab "github.com/kyma-project/kyma/components/application-broker/pkg/client/clientset/versioned"
	ao "github.com/kyma-project/kyma/components/application-operator/pkg/client/clientset/versioned"
	eaClientSet "github.com/kyma-project/kyma/components/event-bus/generated/ea/clientset/versioned"
	subscriptionClientSet "github.com/kyma-project/kyma/components/event-bus/generated/push/clientset/versioned"
	bu "github.com/kyma-project/kyma/components/service-binding-usage-controller/pkg/client/clientset/versioned"
	"github.com/kyma-project/kyma/tests/end-to-end/upgrade/internal/platform/logger"
	"github.com/kyma-project/kyma/tests/end-to-end/upgrade/internal/platform/signal"
	"github.com/kyma-project/kyma/tests/end-to-end/upgrade/internal/runner"
	applicationOperator "github.com/kyma-project/kyma/tests/end-to-end/upgrade/pkg/tests/application-operator"
	assetStore "github.com/kyma-project/kyma/tests/end-to-end/upgrade/pkg/tests/asset-store"
	"github.com/kyma-project/kyma/tests/end-to-end/upgrade/pkg/tests/cms"
	eventBus "github.com/kyma-project/kyma/tests/end-to-end/upgrade/pkg/tests/event-bus"
	"github.com/kyma-project/kyma/tests/end-to-end/upgrade/pkg/tests/function"
	"github.com/kyma-project/kyma/tests/end-to-end/upgrade/pkg/tests/monitoring"
	serviceCatalog "github.com/kyma-project/kyma/tests/end-to-end/upgrade/pkg/tests/service-catalog"
	"github.com/kyma-project/kyma/tests/end-to-end/upgrade/pkg/tests/ui"
)

// Config holds application configuration
type Config struct {
	Logger              logger.Config
	DexUserSecret       string `envconfig:"default=admin-user"`
	DexNamespace        string `envconfig:"default=kyma-system"`
	MaxConcurrencyLevel int    `envconfig:"default=1"`
	KubeconfigPath      string `envconfig:"optional"`
}

const (
	prepareDataActionName  = "prepareData"
	executeTestsActionName = "executeTests"
)

func main() {
	actionUsage := fmt.Sprintf("Define what kind of action runner should execute. Possible values: %s or %s", prepareDataActionName, executeTestsActionName)

	var action string
	var verbose bool
	flag.StringVar(&action, "action", "", actionUsage)
	flag.BoolVar(&verbose, "verbose", false, "Print all test logs")
	flag.Parse()

	var cfg Config
	err := envconfig.InitWithPrefix(&cfg, "APP")
	fatalOnError(err, "while reading configuration from environment variables")

	log := logger.New(&cfg.Logger)

	// Set up signals so we can handle the first shutdown signal gracefully
	stopCh := signal.SetupChannel()

	// K8s client
	k8sConfig, err := newRestClientConfig(cfg.KubeconfigPath)
	fatalOnError(err, "while creating k8s client cfg")

	k8sCli, err := k8sClientSet.NewForConfig(k8sConfig)
	fatalOnError(err, "while creating k8s clientset")

	scCli, err := sc.NewForConfig(k8sConfig)
	fatalOnError(err, "while creating Service Catalog clientset")

	buCli, err := bu.NewForConfig(k8sConfig)
	fatalOnError(err, "while creating Binding Usage clientset")

	appConnectorCli, err := ao.NewForConfig(k8sConfig)
	fatalOnError(err, "while creating Application Connector clientset")

	appBrokerCli, err := ab.NewForConfig(k8sConfig)
	fatalOnError(err, "while creating Application Broker clientset")

	gatewayCli, err := gateway.NewForConfig(k8sConfig)
	fatalOnError(err, "while creating Gateway clientset")

	kubelessCli, err := kubeless.NewForConfig(k8sConfig)
	fatalOnError(err, "while creating Kubeless clientset")

	domainName, err := getDomainNameFromCluster(k8sCli)
	fatalOnError(err, "while reading domain name from cluster")

	subCli, err := subscriptionClientSet.NewForConfig(k8sConfig)
	fatalOnError(err, "while creating Subscription clientset")

	eaCli, err := eaClientSet.NewForConfig(k8sConfig)
	fatalOnError(err, "while creating Event Activation clientset")

	kymaAPI, err := kyma.NewForConfig(k8sConfig)
	fatalOnError(err, "while creating Kyma Api clientset")

	mfCli, err := mfClient.NewForConfig(k8sConfig)
	fatalOnError(err, "while creating Microfrontends clientset")

	dynamicCli, err := dynamic.NewForConfig(k8sConfig)
	fatalOnError(err, "while creating K8s Dynamic client")

	dexConfig, err := getDexConfigFromCluster(k8sCli, cfg.DexUserSecret, cfg.DexNamespace, domainName)
	fatalOnError(err, "while reading dex config from cluster")

	// Register tests. Convention:
	// <test-name> : <test-instance>

	// Using map is on purpose - we ensure that test name will not be duplicated.
	// Test name is sanitized and used for creating dedicated namespace for given test,
	// so it cannot overlap with others.

	grafanaUpgradeTest := monitoring.NewGrafanaUpgradeTest(k8sCli)

	metricUpgradeTest, err := monitoring.NewMetricsUpgradeTest(k8sCli)
	fatalOnError(err, "while creating Metrics Upgrade Test")

	tests := map[string]runner.UpgradeTest{
		"HelmBrokerUpgradeTest":           serviceCatalog.NewHelmBrokerTest(k8sCli, scCli, buCli),
		"ApplicationBrokerUpgradeTest":    serviceCatalog.NewAppBrokerUpgradeTest(scCli, k8sCli, buCli, appBrokerCli, appConnectorCli),
		"LambdaFunctionUpgradeTest":       function.NewLambdaFunctionUpgradeTest(kubelessCli, k8sCli, kymaAPI, domainName),
		"GrafanaUpgradeTest":              grafanaUpgradeTest,
		"MetricsUpgradeTest":              metricUpgradeTest,
		"MicrofrontendUpgradeTest":        ui.NewMicrofrontendUpgradeTest(mfCli),
		"ClusterMicrofrontendUpgradeTest": ui.NewClusterMicrofrontendUpgradeTest(mfCli),
		"EventBusUpgradeTest":             eventBus.NewEventBusUpgradeTest(k8sCli, eaCli, subCli),
		"NamespaceUpgradeTest":            namespaceController.New(k8sCli),
		"AssetStoreUpgradeTest":           assetStore.NewAssetStoreUpgradeTest(dynamicCli),
		"HeadlessCMSUpgradeTest":          cms.NewHeadlessCmsUpgradeTest(dynamicCli),
		"ApiControllerUpgradeTest":        apiController.New(gatewayCli, k8sCli, kubelessCli, domainName, dexConfig.IdProviderConfig()),
		"ApplicationOperatorUpgradeTest":  applicationOperator.NewApplicationOperatorUpgradeTest(appConnectorCli, *k8sCli),
	}

	// Execute requested action
	testRunner, err := runner.NewTestRunner(log, k8sCli.CoreV1().Namespaces(), tests, cfg.MaxConcurrencyLevel, verbose)
	fatalOnError(err, "while creating test runner")

	switch action {
	case prepareDataActionName:
		err := testRunner.PrepareData(stopCh)
		fatalOnError(err, "while executing prepare data for all registered tests")
	case executeTestsActionName:
		err := testRunner.ExecuteTests(stopCh)
		fatalOnError(err, "while executing tests for all registered tests")
	default:
		logrus.Fatalf("Unrecognized runner action. Allowed actions: %s or %s.", prepareDataActionName, executeTestsActionName)
	}
}

func fatalOnError(err error, context string) {
	if err != nil {
		logrus.Fatal(fmt.Sprintf("%s: %v", context, err))
	}
}

func newRestClientConfig(kubeConfigPath string) (*restClient.Config, error) {
	if kubeConfigPath != "" {
		return clientcmd.BuildConfigFromFlags("", kubeConfigPath)
	}

	return restClient.InClusterConfig()
}

func getDomainNameFromCluster(k8sCli *k8sClientSet.Clientset) (string, error) {
	overridesData := overrides.New(k8sCli)

	coreOverridesYaml, err := overridesData.ForRelease("core")
	if err != nil {
		return "", err
	}

	coreOverridesMap, err := overrides.ToMap(coreOverridesYaml)
	if err != nil {
		return "", err
	}

	value, _ := overrides.FindOverrideStringValue(coreOverridesMap, "global.ingress.domainName")
	return value, nil
}

func getDexConfigFromCluster(k8sCli *k8sClientSet.Clientset, userSecret, dexNamespace, domainName string) (dex.Config, error) {
	secret, err := k8sCli.CoreV1().Secrets(dexNamespace).Get(userSecret, metav1.GetOptions{})
	if err != nil {
		return dex.Config{}, err
	}
	dexConfig := dex.Config{
		Domain:       domainName,
		UserEmail:    string(secret.Data["email"]),
		UserPassword: string(secret.Data["password"]),
	}
	return dexConfig, nil
}
