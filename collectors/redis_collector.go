package collectors

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type RedisCollector struct {
	client         kubernetes.Interface
	checker        func(host string, port int32, password string) (alive, healthy, latency float64)
	namespace      string
	serviceDomain  string
	collectorLabel string

	instanceAliveMetric   *prometheus.GaugeVec
	instanceHealthyMetric *prometheus.GaugeVec
	instanceLatencyMetric *prometheus.GaugeVec
}

func NewRedisCollector(
	client kubernetes.Interface,
	checker func(host string, port int32, password string) (alive, healthy, latency float64),
	metricsNamespace string,
	environment string,
	kubernetesNamespace string,
	serviceDomain string,
	collectorLabel string,
) *RedisCollector {
	return &RedisCollector{
		client:         client,
		checker:        checker,
		namespace:      kubernetesNamespace,
		serviceDomain:  serviceDomain,
		collectorLabel: collectorLabel,
		instanceAliveMetric: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricsNamespace,
				Subsystem: "redis",
				Name:      "alive",
				Help:      "Redis service available",
				ConstLabels: prometheus.Labels{
					"environment": environment,
				},
			},
			[]string{"service_guid", "plan_guid", "instance_guid"},
		),
		instanceHealthyMetric: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricsNamespace,
				Subsystem: "redis",
				Name:      "healthy",
				Help:      "Redis service healthy",
				ConstLabels: prometheus.Labels{
					"environment": environment,
				},
			},
			[]string{"service_guid", "plan_guid", "instance_guid"},
		),
		instanceLatencyMetric: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricsNamespace,
				Subsystem: "redis",
				Name:      "latency",
				Help:      "Redis service latency in milliseconds",
				ConstLabels: prometheus.Labels{
					"environment": environment,
				},
			},
			[]string{"service_guid", "plan_guid", "instance_guid"},
		),
	}
}

func (c *RedisCollector) Describe(ch chan<- *prometheus.Desc) {
	c.instanceAliveMetric.Describe(ch)
	c.instanceHealthyMetric.Describe(ch)
	c.instanceLatencyMetric.Describe(ch)
}

func (c *RedisCollector) Collect(ch chan<- prometheus.Metric) error {
	c.instanceAliveMetric.Reset()
	c.instanceHealthyMetric.Reset()
	c.instanceLatencyMetric.Reset()

	services, err := c.client.CoreV1().Services(c.namespace).List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("collector = %s", c.collectorLabel),
	})
	if err != nil {
		return err
	}

	secrets, err := c.client.CoreV1().Secrets(c.namespace).List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("collector = %s", c.collectorLabel),
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

			alive, healthy, latency := c.checker(
				fmt.Sprintf("%s.%s", service.Name, c.serviceDomain),
				ports[6379],
				values["redis-password"],
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

func CheckRedisHealth(host string, port int32, password string) (alive, healthy, latency float64) {
	log.Infof("Sending INFO to redis server at %s:%d", host, port)

	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", host, port),
		Password: password,
	})

	start := time.Now()
	info, err := client.Info("replication").Result()
	latency = time.Since(start).Seconds() * 1000

	if err != nil {
		log.Errorf("Error getting info from redis server: %s", err.Error())
		return
	}

	alive = 1

	parsed := map[string]string{}
	for _, line := range strings.Split(info, "\r\n") {
		pair := strings.Split(line, ":")
		if len(pair) == 2 {
			parsed[pair[0]] = pair[1]
		}
	}

	if parsed["role"] != "master" {
		log.Errorf("Expected role master at %s:%d; got %s", host, port, parsed["role"])
		return
	}

	connected, err := strconv.Atoi(parsed["connected_slaves"])
	if err != nil || connected == 0 {
		log.Errorf("Expected >=0 connected slaves at %s:%d; got %s", host, port, parsed["connected_slaves"])
		return
	}

	healthy = 1
	return
}
