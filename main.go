package main

import (
	"crypto/tls"
	"flag"
	"net/http"
	"os"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/18F/kubernetes-broker-exporter/collectors"
)

var (
	kubernetesURL             = flag.String("kubernetes.url", "", "Kubernetes URL")
	kubernetesUsername        = flag.String("kubernetes.username", "", "Kubernetes username")
	kubernetesPassword        = flag.String("kubernetes.password", "", "Kubernetes password")
	kubernetesNamespace       = flag.String("kubernetes.namespace", "default", "Kubernetes namespace")
	consulDomain              = flag.String("consul.domain", "service.kubernetes", "Consul service domain")
	elasticsearchServiceGUIDs = flag.String("elasticsearch.services", "", "Elasticsearch service GUIDs")
	metricsPath               = flag.String("web.telemetry-path", "/metrics", "Path under which to expose Prometheus metrics")
	listenAddress             = flag.String("web.listen-address", ":9191", "Address to listen on for web interface and telemetry")
	authUsername              = flag.String("web.auth.username", "", "Username for web interface basic auth")
	authPassword              = flag.String("web.auth.password", "", "Password for web interface basic auth")
	metricsNamespace          = flag.String("metrics.namespace", "kubernetes_broker", "Metrics Namespace")
)

func overrideFlagsWithEnvVars() {
	overrideWithEnvVar("BROKER_EXPORTER_KUBERNETES_URL", kubernetesURL)
	overrideWithEnvVar("BROKER_EXPORTER_KUBERNETES_USERNAME", kubernetesUsername)
	overrideWithEnvVar("BROKER_EXPORTER_KUBERNETES_PASSWORD", kubernetesPassword)
	overrideWithEnvVar("BROKER_EXPORTER_KUBERNETES_NAMESPACE", kubernetesNamespace)
	overrideWithEnvVar("BROKER_EXPORTER_CONSUL_DOMAIN", consulDomain)
	overrideWithEnvVar("BROKER_EXPORTER_ELASTICSEARCH_SERVICES", elasticsearchServiceGUIDs)
	overrideWithEnvVar("BROKER_EXPORTER_WEB_METRICS_PATH", metricsPath)
	overrideWithEnvVar("BROKER_EXPORTER_WEB_LISTEN_ADDRESS", listenAddress)
	overrideWithEnvVar("BROKER_EXPORTER_WEB_AUTH_USERNAME", authUsername)
	overrideWithEnvVar("BROKER_EXPORTER_WEB_AUTH_PASSWORD", authPassword)
	overrideWithEnvVar("BROKER_EXPORTER_METRICS_NAMESPACE", metricsNamespace)
}

func overrideWithEnvVar(name string, value *string) {
	envValue := os.Getenv(name)
	if envValue != "" {
		*value = envValue
	}
}

type basicAuthHandler struct {
	handler  http.HandlerFunc
	username string
	password string
}

func (h *basicAuthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	username, password, ok := r.BasicAuth()
	if !ok || username != h.username || password != h.password {
		log.Errorf("Invalid HTTP auth from `%s`", r.RemoteAddr)
		w.Header().Set("WWW-Authenticate", "Basic realm=\"metrics\"")
		http.Error(w, "Invalid username or password", http.StatusUnauthorized)
		return
	}
	h.handler(w, r)
	return
}

func prometheusHandler() http.Handler {
	handler := prometheus.Handler()

	if *authUsername != "" && *authPassword != "" {
		handler = &basicAuthHandler{
			handler:  prometheus.Handler().ServeHTTP,
			username: *authUsername,
			password: *authPassword,
		}
	}

	return handler
}

func main() {
	flag.Parse()
	overrideFlagsWithEnvVars()

	client, err := kubernetes.NewForConfig(&rest.Config{
		Host:     *kubernetesURL,
		Username: *kubernetesUsername,
		Password: *kubernetesPassword,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	})
	if err != nil {
		log.Errorf("Error creating kubernetes client: %s", err.Error())
		os.Exit(1)
	}

	collector := collectors.NewBrokerCollector(
		[]collectors.Collector{
			collectors.NewElasticsearchCollector(
				client,
				collectors.CheckElasticsearchHealth,
				*metricsNamespace,
				*kubernetesNamespace,
				*consulDomain,
				strings.Split(*elasticsearchServiceGUIDs, ","),
			),
		},
	)

	prometheus.MustRegister(collector)

	http.Handle(*metricsPath, prometheusHandler())
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
			<head><title>Kubernetes Broker Exporter</title></head>
			<body>
				<h1>Kubernetes Broker Exporter</h1>
				<p><a href="` + *metricsPath + `">Metrics</a></p>
			</body>
		</html>`))
	})

	log.Infoln("Listening on", *listenAddress)
	log.Fatal(http.ListenAndServe(*listenAddress, nil))
}
