package collectors_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/prometheus/client_golang/prometheus"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/18f/kubernetes-broker-exporter/collectors"
)

var _ = Describe("RedisCollector", func() {
	var (
		client              kubernetes.Interface
		checker             func(host string, port int32, password string) (alive, healthy, latency float64)
		metricsNamespace    string
		metricsEnvironment  string
		kubernetesNamespace string
		serviceDomain       string
		collectorLabel      string
		redisCollector      *collectors.RedisCollector

		instanceAliveMetric   *prometheus.GaugeVec
		instanceHealthyMetric *prometheus.GaugeVec
		instanceLatencyMetric *prometheus.GaugeVec

		catalogServiceID string
		catalogPlanID    string
		serviceID        string

		alive   float64
		healthy float64
		latency float64
	)

	BeforeEach(func() {
		metricsNamespace = "test_exporter"
		metricsEnvironment = "test"
		kubernetesNamespace = "default"
		serviceDomain = "service.kubernetes"
		collectorLabel = "redis"

		catalogServiceID = "service-guid"
		catalogPlanID = "plan-guid"
		serviceID = "instance-guid"
	})

	JustBeforeEach(func() {
		redisCollector = collectors.NewRedisCollector(
			client,
			checker,
			metricsNamespace,
			metricsEnvironment,
			kubernetesNamespace,
			serviceDomain,
			collectorLabel,
		)

		instanceAliveMetric = prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricsNamespace,
				Subsystem: "redis",
				Name:      "alive",
				Help:      "Redis service available",
				ConstLabels: prometheus.Labels{
					"environment": metricsEnvironment,
				},
			},
			[]string{"service_guid", "plan_guid", "instance_guid"},
		)
		instanceAliveMetric.WithLabelValues(
			catalogServiceID,
			catalogPlanID,
			serviceID,
		).Set(alive)

		instanceHealthyMetric = prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricsNamespace,
				Subsystem: "redis",
				Name:      "healthy",
				Help:      "Redis service healthy",
				ConstLabels: prometheus.Labels{
					"environment": metricsEnvironment,
				},
			},
			[]string{"service_guid", "plan_guid", "instance_guid"},
		)
		instanceHealthyMetric.WithLabelValues(
			catalogServiceID,
			catalogPlanID,
			serviceID,
		).Set(float64(healthy))

		instanceLatencyMetric = prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricsNamespace,
				Subsystem: "redis",
				Name:      "latency",
				Help:      "Redis service latency in milliseconds",
				ConstLabels: prometheus.Labels{
					"environment": metricsEnvironment,
				},
			},
			[]string{"service_guid", "plan_guid", "instance_guid"},
		)
		instanceLatencyMetric.WithLabelValues(
			catalogServiceID,
			catalogPlanID,
			serviceID,
		).Set(float64(latency))
	})

	Describe("Collect", func() {
		var (
			metrics    chan prometheus.Metric
			errMetrics chan error
		)

		BeforeEach(func() {
			metrics = make(chan prometheus.Metric)
			errMetrics = make(chan error, 1)
		})

		JustBeforeEach(func() {
			go func() {
				if err := redisCollector.Collect(metrics); err != nil {
					errMetrics <- err
				}
			}()
		})

		Context("kubernetes returns services and secrets", func() {
			BeforeEach(func() {
				alive = 1.0
				healthy = 1.0
				latency = 100.0

				client = fake.NewSimpleClientset(
					&apiv1.ServiceList{
						Items: []apiv1.Service{
							{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "service",
									Namespace: "default",
									Labels: map[string]string{
										"collector":          collectorLabel,
										"catalog_service_id": catalogServiceID,
										"catalog_plan_id":    catalogPlanID,
										"service_id":         serviceID,
									},
								},
								Spec: apiv1.ServiceSpec{
									Type: apiv1.ServiceTypeNodePort,
								},
							},
						},
					},
					&apiv1.SecretList{
						Items: []apiv1.Secret{
							{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "secret",
									Namespace: "default",
								},
								Data: map[string][]byte{
									"password": []byte("donttell"),
								},
							},
						},
					},
				)
				checker = func(host string, port int32, password string) (float64, float64, float64) {
					return alive, healthy, latency
				}
			})

			It("receives an alive metric", func() {
				Eventually(metrics).Should(Receive(Equal(instanceAliveMetric.WithLabelValues(
					catalogServiceID,
					catalogPlanID,
					serviceID,
				))))
				Consistently(errMetrics).ShouldNot(Receive())
			})

			It("receives a healthy metric", func() {
				Eventually(metrics).Should(Receive(Equal(instanceHealthyMetric.WithLabelValues(
					catalogServiceID,
					catalogPlanID,
					serviceID,
				))))
				Consistently(errMetrics).ShouldNot(Receive())
			})

			It("receives a latency metric", func() {
				Eventually(metrics).Should(Receive(Equal(instanceLatencyMetric.WithLabelValues(
					catalogServiceID,
					catalogPlanID,
					serviceID,
				))))
				Consistently(errMetrics).ShouldNot(Receive())
			})
		})
	})
})
