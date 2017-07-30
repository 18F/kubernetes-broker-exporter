package collectors

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"
)

type Collector interface {
	Collect(ch chan<- prometheus.Metric) error
	Describe(ch chan<- *prometheus.Desc)
}

type BrokerCollector struct {
	collectors []Collector
}

func NewBrokerCollector(
	collectors []Collector,
) *BrokerCollector {
	return &BrokerCollector{
		collectors: collectors,
	}
}

func (c *BrokerCollector) Describe(ch chan<- *prometheus.Desc) {
	wg := &sync.WaitGroup{}
	for _, collector := range c.collectors {
		wg.Add(1)
		go func(collector Collector) {
			defer wg.Done()
			collector.Describe(ch)
		}(collector)
	}
	wg.Wait()
}

func (c *BrokerCollector) Collect(ch chan<- prometheus.Metric) {
	if err := c.collect(ch); err != nil {
		log.Error(err)
	}
}

func (c *BrokerCollector) collect(ch chan<- prometheus.Metric) error {
	wg := &sync.WaitGroup{}
	doneChannel := make(chan bool, 1)
	errChannel := make(chan error, 1)

	for _, collector := range c.collectors {
		wg.Add(1)
		go func(collector Collector) {
			defer wg.Done()
			if err := collector.Collect(ch); err != nil {
				errChannel <- err
			}
		}(collector)
	}

	go func() {
		wg.Wait()
		close(doneChannel)
	}()

	select {
	case <-doneChannel:
	case err := <-errChannel:
		return err
	}

	return nil
}
