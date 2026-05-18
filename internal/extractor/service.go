package extractor

import (
	"context"
	"strconv"

	"kube-insight/internal/core"
)

type ServiceExtractor struct{}

func (ServiceExtractor) Kind() string { return "Service" }

func (ServiceExtractor) Extract(_ context.Context, obs core.Observation) (Evidence, error) {
	var out Evidence
	serviceType, _ := stringAt(obs.Object, "spec", "type")
	if serviceType != "" {
		out.Facts = append(out.Facts, fact(obs, "service.type", serviceType, 10))
		out.Changes = append(out.Changes, change(obs, "spec", "spec.type", "replace", "", serviceType, 10))
	}
	if clusterIP, ok := stringAt(obs.Object, "spec", "clusterIP"); ok && clusterIP != "" && clusterIP != "None" {
		out.Facts = append(out.Facts, fact(obs, "service.cluster_ip", clusterIP, 10))
	}
	if serviceType == "LoadBalancer" {
		appendLoadBalancerEvidence(obs, &out)
	}
	if obs.Type == core.ObservationDeleted {
		out.Facts = append(out.Facts, fact(obs, "service.deleted", "true", 60))
		out.Changes = append(out.Changes, change(obs, "status", "metadata.deletionTimestamp", "delete", "", "deleted", 60))
	}
	return out, nil
}

func appendLoadBalancerEvidence(obs core.Observation, out *Evidence) {
	status, _ := obs.Object["status"].(map[string]any)
	loadBalancer, _ := status["loadBalancer"].(map[string]any)
	ingress, _ := loadBalancer["ingress"].([]any)
	countValue := float64(len(ingress))
	countFact := fact(obs, "service.load_balancer.ingress_count", strconv.Itoa(len(ingress)), 10)
	countFact.NumericValue = &countValue
	out.Facts = append(out.Facts, countFact)
	if len(ingress) == 0 {
		out.Facts = append(out.Facts, fact(obs, "service.load_balancer.pending", "true", 50))
		out.Changes = append(out.Changes, change(obs, "status", "status.loadBalancer.ingress", "replace", "", "pending", 50))
		return
	}
	out.Facts = append(out.Facts, fact(obs, "service.load_balancer.pending", "false", 10))
	for _, raw := range ingress {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		ip, _ := item["ip"].(string)
		hostname, _ := item["hostname"].(string)
		ipMode, _ := item["ipMode"].(string)
		detail := map[string]any{}
		if ipMode != "" {
			detail["ipMode"] = ipMode
		}
		if ip != "" {
			f := fact(obs, "service.load_balancer.ingress_ip", ip, 10)
			if len(detail) > 0 {
				f.Detail = detail
			}
			out.Facts = append(out.Facts, f)
			out.Changes = append(out.Changes, change(obs, "status", "status.loadBalancer.ingress.ip", "replace", "", ip, 10))
		}
		if hostname != "" {
			f := fact(obs, "service.load_balancer.ingress_hostname", hostname, 10)
			if len(detail) > 0 {
				f.Detail = detail
			}
			out.Facts = append(out.Facts, f)
			out.Changes = append(out.Changes, change(obs, "status", "status.loadBalancer.ingress.hostname", "replace", "", hostname, 10))
		}
	}
}
