# kubernetes-broker-exporter

Goals:
* Report service metrics (availability, health, latency) on brokered Kubernetes services to Prometheus
* Check service health from the perspective of the user, via a Cloud Foundry application

Supported services:
* elasticsearch
* redis
