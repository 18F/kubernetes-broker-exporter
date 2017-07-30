package collectors

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type ElasticsearchCollector struct {
	client                kubernetes.Interface
	namespace             string
	serviceDomain         string
	serviceGUIDs          []string
	instanceAliveMetric   *prometheus.GaugeVec
	instanceHealthyMetric *prometheus.GaugeVec
	instanceLatencyMetric *prometheus.GaugeVec
}

func NewElasticsearchCollector(
	client kubernetes.Interface,
	metricsNamespace string,
	kubernetesNamespace string,
	serviceDomain string,
	serviceGUIDs []string,
) *ElasticsearchCollector {
	return &ElasticsearchCollector{
		client:        client,
		namespace:     kubernetesNamespace,
		serviceDomain: serviceDomain,
		serviceGUIDs:  serviceGUIDs,
		instanceAliveMetric: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricsNamespace,
				Subsystem: "elasticsearch",
				Name:      "healthy",
				Help:      "Elasticsearch service healthy",
			},
			[]string{"service_guid", "plan_guid", "instance_guid"},
		),
		instanceHealthyMetric: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricsNamespace,
				Subsystem: "elasticsearch",
				Name:      "alive",
				Help:      "Elasticsearch service available",
			},
			[]string{"service_guid", "plan_guid", "instance_guid"},
		),
		instanceLatencyMetric: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricsNamespace,
				Subsystem: "elasticsearch",
				Name:      "latency",
				Help:      "Elasticsearch service latency",
			},
			[]string{"service_guid", "plan_guid", "instance_guid"},
		),
	}
}

func (c *ElasticsearchCollector) Describe(ch chan<- *prometheus.Desc) {
	c.instanceAliveMetric.Describe(ch)
	c.instanceHealthyMetric.Describe(ch)
	c.instanceLatencyMetric.Describe(ch)
}

func (c *ElasticsearchCollector) Collect(ch chan<- prometheus.Metric) error {
	c.instanceAliveMetric.Reset()
	c.instanceHealthyMetric.Reset()
	c.instanceLatencyMetric.Reset()

	services, err := c.client.CoreV1().Services(c.namespace).List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("catalog_service_id in (%s)", strings.Join(c.serviceGUIDs, ",")),
	})
	if err != nil {
		return err
	}

	secrets, err := c.client.CoreV1().Secrets(c.namespace).List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("catalog_service_id in (%s)", strings.Join(c.serviceGUIDs, ",")),
	})
	if err != nil {
		return err
	}

	serviceSecrets := map[string][]apiv1.Secret{}
	for _, secret := range secrets.Items {
		serviceID := secret.ObjectMeta.Labels["service_id"]
		serviceSecrets[serviceID] = append(serviceSecrets[serviceID], secret)
	}

	wg := &sync.WaitGroup{}
	for _, service := range services.Items {
		// Skip discovery service
		if service.Spec.Type != apiv1.ServiceTypeNodePort {
			continue
		}
		wg.Add(1)
		go func(service apiv1.Service) {
			defer wg.Done()
			values := map[string]string{}
			for _, secret := range serviceSecrets[service.ObjectMeta.Labels["service_id"]] {
				for key, value := range secret.Data {
					values[key] = string(value)
				}
			}

			ports := map[int32]int32{}
			for _, port := range service.Spec.Ports {
				ports[port.Port] = port.NodePort
			}

			alive, healthy, latency := c.clusterHealth(
				fmt.Sprintf("%s.%s", service.Name, c.serviceDomain),
				ports[9200],
				values["password"],
			)

			c.instanceAliveMetric.WithLabelValues(
				service.ObjectMeta.Labels["catalog_service_id"],
				service.ObjectMeta.Labels["catalog_plan_id"],
				service.ObjectMeta.Labels["service_id"],
			).Set(alive)
			c.instanceHealthyMetric.WithLabelValues(
				service.ObjectMeta.Labels["catalog_service_id"],
				service.ObjectMeta.Labels["catalog_plan_id"],
				service.ObjectMeta.Labels["service_id"],
			).Set(healthy)
			c.instanceLatencyMetric.WithLabelValues(
				service.ObjectMeta.Labels["catalog_service_id"],
				service.ObjectMeta.Labels["catalog_plan_id"],
				service.ObjectMeta.Labels["service_id"],
			).Set(latency)
		}(service)
	}
	wg.Wait()

	c.instanceAliveMetric.Collect(ch)
	c.instanceHealthyMetric.Collect(ch)
	c.instanceLatencyMetric.Collect(ch)

	return nil
}

type clusterHealthResponse struct {
	Status string `json:"status"`
}

func (c *ElasticsearchCollector) clusterHealth(host string, port int32, password string) (alive float64, healthy float64, latency float64) {
	client := &http.Client{Timeout: time.Second * 60}

	log.Infof("Checking cluster health at http://root:%s@%s:%d/_cluster/health", password, host, port)
	req, err := http.NewRequest("GET", fmt.Sprintf("http://%s:%d/_cluster/health", host, port), nil)
	if err != nil {
		log.Errorf("Invalid HTTP auth from `%s`", err.Error())
		return
	}
	req.SetBasicAuth("root", password)

	start := time.Now()
	resp, err := client.Do(req)
	latency = time.Since(start).Seconds()

	if err != nil || resp.StatusCode != http.StatusOK {
		log.Errorf("Invalid HTTP auth from `%s`", err.Error())
		return
	}

	alive = 1
	defer resp.Body.Close()

	var health clusterHealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return
	}

	if health.Status != "red" {
		healthy = 1
	}
	return
}
