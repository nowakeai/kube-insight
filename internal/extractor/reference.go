package extractor

import (
	"context"

	"kube-insight/internal/core"
)

type ReferenceExtractor struct{}

func (ReferenceExtractor) Kind() string { return "*" }

func (ReferenceExtractor) Extract(ctx context.Context, obs core.Observation) (Evidence, error) {
	var out Evidence
	metadata, _ := obs.Object["metadata"].(map[string]any)
	if refs, ok := metadata["ownerReferences"].([]any); ok {
		for _, item := range refs {
			refMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			ref, ok := objectRefFromMap(ctx, refMap, obs.Ref.Namespace)
			if !ok {
				continue
			}
			if edge, ok := edgeToRef(ctx, obs, "owned_by", ref); ok {
				out.Edges = append(out.Edges, edge)
			}
		}
	}

	spec, _ := obs.Object["spec"].(map[string]any)
	switch obs.Ref.Kind {
	case "Certificate":
		appendCertManagerCertificateEdges(ctx, obs, spec, &out)
	case "CertificateRequest":
		appendCertManagerCertificateRequestEdges(ctx, obs, spec, &out)
	case "Order":
		appendNamedRefEdge(ctx, obs, &out, "certmanager_references_certificaterequest", "cert-manager.io", "CertificateRequest", obs.Ref.Namespace, specMap(spec, "requestRef"), "name")
	case "Challenge":
		appendNamedRefEdge(ctx, obs, &out, "certmanager_references_order", "acme.cert-manager.io", "Order", obs.Ref.Namespace, specMap(spec, "orderRef"), "name")
	case "ValidatingWebhookConfiguration", "MutatingWebhookConfiguration":
		appendWebhookEdges(ctx, obs, &out)
	case "RoleBinding", "ClusterRoleBinding":
		appendRBACBindingEdges(ctx, obs, &out)
	case "Pod":
		appendPodSpecEdges(ctx, obs, spec, "pod", &out)
	case "Deployment", "ReplicaSet", "DaemonSet", "StatefulSet":
		appendWorkloadTemplateEdges(ctx, obs, spec, &out)
	case "Job":
		appendPodTemplateAt(ctx, obs, spec, "template", &out)
	case "CronJob":
		jobTemplate := specMap(spec, "jobTemplate")
		jobSpec := specMap(jobTemplate, "spec")
		appendPodTemplateAt(ctx, obs, jobSpec, "template", &out)
	case "ServiceAccount":
		appendServiceAccountEdges(ctx, obs, &out)
	case "HorizontalPodAutoscaler":
		appendScaleTargetEdge(ctx, obs, spec, &out)
	case "Ingress":
		appendIngressEdges(ctx, obs, spec, &out)
	case "PersistentVolumeClaim":
		appendPersistentVolumeClaimEdges(ctx, obs, spec, &out)
	case "PersistentVolume":
		appendPersistentVolumeEdges(ctx, obs, spec, &out)
	case "CustomResourceDefinition":
		appendCRDConversionWebhookEdges(ctx, obs, spec, &out)
	case "APIService":
		appendAPIServiceEdges(ctx, obs, spec, &out)
	case "Gateway":
		appendGatewayEdges(ctx, obs, spec, &out)
	case "HTTPRoute", "GRPCRoute", "TCPRoute", "TLSRoute", "UDPRoute":
		appendGatewayRouteEdges(ctx, obs, spec, &out)
	}

	return out, nil
}

func appendPodSpecEdges(ctx context.Context, obs core.Observation, spec map[string]any, prefix string, out *Evidence) {
	if serviceAccountName, ok := spec["serviceAccountName"].(string); ok && serviceAccountName != "" {
		appendEdgeToRef(ctx, obs, out, prefix+"_uses_serviceaccount", targetObjectRef{Kind: "ServiceAccount", Namespace: obs.Ref.Namespace, Name: serviceAccountName})
	}
	appendLocalObjectRefList(ctx, obs, out, prefix+"_uses_secret", "Secret", spec["imagePullSecrets"], "name")
	appendVolumeEdges(ctx, obs, spec, prefix, out)
	for _, key := range []string{"initContainers", "containers", "ephemeralContainers"} {
		appendContainerRefEdges(ctx, obs, out, prefix, spec[key])
	}
}

func appendWorkloadTemplateEdges(ctx context.Context, obs core.Observation, spec map[string]any, out *Evidence) {
	appendPodTemplateAt(ctx, obs, spec, "template", out)
}

func appendPodTemplateAt(ctx context.Context, obs core.Observation, spec map[string]any, key string, out *Evidence) {
	template := specMap(spec, key)
	templateSpec := specMap(template, "spec")
	appendPodSpecEdges(ctx, obs, templateSpec, "workload_template", out)
}

func appendVolumeEdges(ctx context.Context, obs core.Observation, spec map[string]any, prefix string, out *Evidence) {
	volumes, ok := spec["volumes"].([]any)
	if !ok {
		return
	}
	for _, item := range volumes {
		volume, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if secret := specMap(volume, "secret"); len(secret) > 0 {
			appendNamedRefEdge(ctx, obs, out, prefix+"_uses_secret", "", "Secret", obs.Ref.Namespace, secret, "secretName")
		}
		if configMap := specMap(volume, "configMap"); len(configMap) > 0 {
			appendNamedRefEdge(ctx, obs, out, prefix+"_uses_configmap", "", "ConfigMap", obs.Ref.Namespace, configMap, "name")
		}
		if pvc := specMap(volume, "persistentVolumeClaim"); len(pvc) > 0 {
			appendNamedRefEdge(ctx, obs, out, prefix+"_uses_pvc", "", "PersistentVolumeClaim", obs.Ref.Namespace, pvc, "claimName")
		}
		if projected := specMap(volume, "projected"); len(projected) > 0 {
			appendProjectedSourceEdges(ctx, obs, projected["sources"], prefix, out)
		}
	}
}

func appendProjectedSourceEdges(ctx context.Context, obs core.Observation, sources any, prefix string, out *Evidence) {
	items, ok := sources.([]any)
	if !ok {
		return
	}
	for _, item := range items {
		source, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if secret := specMap(source, "secret"); len(secret) > 0 {
			appendNamedRefEdge(ctx, obs, out, prefix+"_uses_secret", "", "Secret", obs.Ref.Namespace, secret, "name")
		}
		if configMap := specMap(source, "configMap"); len(configMap) > 0 {
			appendNamedRefEdge(ctx, obs, out, prefix+"_uses_configmap", "", "ConfigMap", obs.Ref.Namespace, configMap, "name")
		}
	}
}

func appendContainerRefEdges(ctx context.Context, obs core.Observation, out *Evidence, prefix string, containers any) {
	items, ok := containers.([]any)
	if !ok {
		return
	}
	for _, item := range items {
		container, ok := item.(map[string]any)
		if !ok {
			continue
		}
		appendEnvFromEdges(ctx, obs, out, prefix, container["envFrom"])
		appendEnvValueEdges(ctx, obs, out, prefix, container["env"])
	}
}

func appendEnvFromEdges(ctx context.Context, obs core.Observation, out *Evidence, prefix string, envFrom any) {
	items, ok := envFrom.([]any)
	if !ok {
		return
	}
	for _, item := range items {
		source, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if secret := specMap(source, "secretRef"); len(secret) > 0 {
			appendNamedRefEdge(ctx, obs, out, prefix+"_uses_secret", "", "Secret", obs.Ref.Namespace, secret, "name")
		}
		if configMap := specMap(source, "configMapRef"); len(configMap) > 0 {
			appendNamedRefEdge(ctx, obs, out, prefix+"_uses_configmap", "", "ConfigMap", obs.Ref.Namespace, configMap, "name")
		}
	}
}

func appendEnvValueEdges(ctx context.Context, obs core.Observation, out *Evidence, prefix string, env any) {
	items, ok := env.([]any)
	if !ok {
		return
	}
	for _, item := range items {
		envVar, ok := item.(map[string]any)
		if !ok {
			continue
		}
		valueFrom := specMap(envVar, "valueFrom")
		if secret := specMap(valueFrom, "secretKeyRef"); len(secret) > 0 {
			appendNamedRefEdge(ctx, obs, out, prefix+"_uses_secret", "", "Secret", obs.Ref.Namespace, secret, "name")
		}
		if configMap := specMap(valueFrom, "configMapKeyRef"); len(configMap) > 0 {
			appendNamedRefEdge(ctx, obs, out, prefix+"_uses_configmap", "", "ConfigMap", obs.Ref.Namespace, configMap, "name")
		}
	}
}

func appendServiceAccountEdges(ctx context.Context, obs core.Observation, out *Evidence) {
	appendLocalObjectRefList(ctx, obs, out, "serviceaccount_uses_secret", "Secret", obs.Object["secrets"], "name")
	appendLocalObjectRefList(ctx, obs, out, "serviceaccount_uses_imagepullsecret", "Secret", obs.Object["imagePullSecrets"], "name")
}

func appendScaleTargetEdge(ctx context.Context, obs core.Observation, spec map[string]any, out *Evidence) {
	ref, ok := objectRefFromMap(ctx, specMap(spec, "scaleTargetRef"), obs.Ref.Namespace)
	if !ok {
		return
	}
	appendEdgeToRef(ctx, obs, out, "hpa_scales_target", ref)
}

func appendIngressEdges(ctx context.Context, obs core.Observation, spec map[string]any, out *Evidence) {
	appendIngressBackendEdge(ctx, obs, specMap(specMap(spec, "defaultBackend"), "service"), out)
	rules, ok := spec["rules"].([]any)
	if !ok {
		return
	}
	for _, item := range rules {
		rule, ok := item.(map[string]any)
		if !ok {
			continue
		}
		http := specMap(rule, "http")
		paths, ok := http["paths"].([]any)
		if !ok {
			continue
		}
		for _, rawPath := range paths {
			path, ok := rawPath.(map[string]any)
			if !ok {
				continue
			}
			appendIngressBackendEdge(ctx, obs, specMap(specMap(path, "backend"), "service"), out)
		}
	}
}

func appendIngressBackendEdge(ctx context.Context, obs core.Observation, service map[string]any, out *Evidence) {
	appendNamedRefEdge(ctx, obs, out, "ingress_routes_to_service", "", "Service", obs.Ref.Namespace, service, "name")
}

func appendPersistentVolumeClaimEdges(ctx context.Context, obs core.Observation, spec map[string]any, out *Evidence) {
	if volumeName, ok := spec["volumeName"].(string); ok && volumeName != "" {
		appendEdgeToRef(ctx, obs, out, "pvc_bound_to_pv", targetObjectRef{Kind: "PersistentVolume", Name: volumeName})
	}
	if storageClassName, ok := spec["storageClassName"].(string); ok && storageClassName != "" {
		appendEdgeToRef(ctx, obs, out, "pvc_uses_storageclass", targetObjectRef{Group: "storage.k8s.io", Kind: "StorageClass", Name: storageClassName})
	}
}

func appendPersistentVolumeEdges(ctx context.Context, obs core.Observation, spec map[string]any, out *Evidence) {
	if claimRef := specMap(spec, "claimRef"); len(claimRef) > 0 {
		ref, ok := objectRefFromMap(ctx, mapWithDefaults(claimRef, "v1", "PersistentVolumeClaim"), "")
		if ok {
			appendEdgeToRef(ctx, obs, out, "pv_bound_to_pvc", ref)
		}
	}
	if storageClassName, ok := spec["storageClassName"].(string); ok && storageClassName != "" {
		appendEdgeToRef(ctx, obs, out, "pv_uses_storageclass", targetObjectRef{Group: "storage.k8s.io", Kind: "StorageClass", Name: storageClassName})
	}
}

func appendCRDConversionWebhookEdges(ctx context.Context, obs core.Observation, spec map[string]any, out *Evidence) {
	conversion := specMap(spec, "conversion")
	webhook := specMap(conversion, "webhook")
	clientConfig := specMap(webhook, "clientConfig")
	service := specMap(clientConfig, "service")
	namespace, _ := service["namespace"].(string)
	appendNamedRefEdge(ctx, obs, out, "crd_conversion_webhook_uses_service", "", "Service", namespace, service, "name")
}

func appendAPIServiceEdges(ctx context.Context, obs core.Observation, spec map[string]any, out *Evidence) {
	service := specMap(spec, "service")
	namespace, _ := service["namespace"].(string)
	appendNamedRefEdge(ctx, obs, out, "apiservice_uses_service", "", "Service", namespace, service, "name")
}

func appendGatewayEdges(ctx context.Context, obs core.Observation, spec map[string]any, out *Evidence) {
	if className, ok := spec["gatewayClassName"].(string); ok && className != "" {
		appendEdgeToRef(ctx, obs, out, "gateway_uses_gatewayclass", targetObjectRef{Group: "gateway.networking.k8s.io", Kind: "GatewayClass", Name: className})
	}
}

func appendGatewayRouteEdges(ctx context.Context, obs core.Observation, spec map[string]any, out *Evidence) {
	appendGatewayParentEdges(ctx, obs, spec["parentRefs"], out)
	appendGatewayBackendEdges(ctx, obs, spec["backendRefs"], out)
	rules, ok := spec["rules"].([]any)
	if !ok {
		return
	}
	for _, item := range rules {
		rule, ok := item.(map[string]any)
		if !ok {
			continue
		}
		appendGatewayBackendEdges(ctx, obs, rule["backendRefs"], out)
	}
}

func appendGatewayParentEdges(ctx context.Context, obs core.Observation, refs any, out *Evidence) {
	items, ok := refs.([]any)
	if !ok {
		return
	}
	for _, item := range items {
		ref, ok := item.(map[string]any)
		if !ok {
			continue
		}
		appendGatewayObjectRefEdge(ctx, obs, out, "gateway_route_attaches_to_parent", ref, "Gateway", "gateway.networking.k8s.io")
	}
}

func appendGatewayBackendEdges(ctx context.Context, obs core.Observation, refs any, out *Evidence) {
	items, ok := refs.([]any)
	if !ok {
		return
	}
	for _, item := range items {
		ref, ok := item.(map[string]any)
		if !ok {
			continue
		}
		appendGatewayObjectRefEdge(ctx, obs, out, "gateway_route_uses_backend", ref, "Service", "")
	}
}

func appendGatewayObjectRefEdge(ctx context.Context, obs core.Observation, out *Evidence, edgeType string, ref map[string]any, defaultKind, defaultGroup string) {
	name, _ := ref["name"].(string)
	kind, _ := ref["kind"].(string)
	if kind == "" {
		kind = defaultKind
	}
	group, _ := ref["group"].(string)
	if group == "" {
		group = defaultGroup
	}
	namespace, _ := ref["namespace"].(string)
	if namespace == "" && resourceNamespacedForKind(ctx, kind) {
		namespace = obs.Ref.Namespace
	}
	appendEdgeToRef(ctx, obs, out, edgeType, targetObjectRef{Group: group, Kind: kind, Namespace: namespace, Name: name})
}

func appendRBACBindingEdges(ctx context.Context, obs core.Observation, out *Evidence) {
	roleRef, _ := obs.Object["roleRef"].(map[string]any)
	roleName, _ := roleRef["name"].(string)
	roleKind, _ := roleRef["kind"].(string)
	if roleKind == "" {
		roleKind = "Role"
	}
	apiGroup, _ := roleRef["apiGroup"].(string)
	if apiGroup == "" {
		apiGroup, _ = roleRef["group"].(string)
	}
	if apiGroup == "" {
		apiGroup = "rbac.authorization.k8s.io"
	}
	roleNamespace := obs.Ref.Namespace
	if roleKind == "ClusterRole" {
		roleNamespace = ""
	}
	appendEdgeToRef(ctx, obs, out, "rbac_binding_grants_role", targetObjectRef{
		Group:     apiGroup,
		Kind:      roleKind,
		Namespace: roleNamespace,
		Name:      roleName,
	})

	subjects, ok := obs.Object["subjects"].([]any)
	if !ok {
		return
	}
	for _, item := range subjects {
		subject, ok := item.(map[string]any)
		if !ok {
			continue
		}
		kind, _ := subject["kind"].(string)
		name, _ := subject["name"].(string)
		namespace, _ := subject["namespace"].(string)
		if kind == "" || name == "" {
			continue
		}
		group := ""
		if kind == "User" || kind == "Group" {
			group = "rbac.authorization.k8s.io"
			namespace = ""
		} else if kind == "ServiceAccount" && namespace == "" {
			namespace = obs.Ref.Namespace
		}
		appendEdgeToRef(ctx, obs, out, "rbac_binding_binds_subject", targetObjectRef{
			Group:     group,
			Kind:      kind,
			Namespace: namespace,
			Name:      name,
		})
	}
}

func appendCertManagerCertificateEdges(ctx context.Context, obs core.Observation, spec map[string]any, out *Evidence) {
	appendIssuerRefEdge(ctx, obs, out, specMap(spec, "issuerRef"))
	if secretName, ok := spec["secretName"].(string); ok {
		appendEdgeToRef(ctx, obs, out, "certmanager_writes_secret", targetObjectRef{Kind: "Secret", Namespace: obs.Ref.Namespace, Name: secretName})
	}
	if privateKeyRef := specMap(spec, "privateKeySecretRef"); len(privateKeyRef) > 0 {
		appendNamedRefEdge(ctx, obs, out, "certmanager_uses_private_key_secret", "", "Secret", obs.Ref.Namespace, privateKeyRef, "name")
	}
}

func appendCertManagerCertificateRequestEdges(ctx context.Context, obs core.Observation, spec map[string]any, out *Evidence) {
	appendIssuerRefEdge(ctx, obs, out, specMap(spec, "issuerRef"))
	appendNamedRefEdge(ctx, obs, out, "certmanager_references_certificate", "cert-manager.io", "Certificate", obs.Ref.Namespace, specMap(spec, "certificateRef"), "name")
}

func appendIssuerRefEdge(ctx context.Context, obs core.Observation, out *Evidence, issuerRef map[string]any) {
	if len(issuerRef) == 0 {
		return
	}
	name, _ := issuerRef["name"].(string)
	kind, _ := issuerRef["kind"].(string)
	if kind == "" {
		kind = "Issuer"
	}
	group, _ := issuerRef["group"].(string)
	if group == "" {
		group = "cert-manager.io"
	}
	namespace := obs.Ref.Namespace
	if kind == "ClusterIssuer" {
		namespace = ""
	}
	appendEdgeToRef(ctx, obs, out, "certmanager_uses_issuer", targetObjectRef{
		Group:     group,
		Kind:      kind,
		Namespace: namespace,
		Name:      name,
	})
}

func appendWebhookEdges(ctx context.Context, obs core.Observation, out *Evidence) {
	webhooks, ok := obs.Object["webhooks"].([]any)
	if !ok {
		return
	}
	for _, item := range webhooks {
		webhook, ok := item.(map[string]any)
		if !ok {
			continue
		}
		clientConfig, _ := webhook["clientConfig"].(map[string]any)
		service, _ := clientConfig["service"].(map[string]any)
		name, _ := service["name"].(string)
		namespace, _ := service["namespace"].(string)
		appendEdgeToRef(ctx, obs, out, "webhook_uses_service", targetObjectRef{
			Kind:      "Service",
			Namespace: namespace,
			Name:      name,
		})
	}
}

func appendNamedRefEdge(ctx context.Context, obs core.Observation, out *Evidence, edgeType, group, kind, namespace string, ref map[string]any, nameKey string) {
	name, _ := ref[nameKey].(string)
	appendEdgeToRef(ctx, obs, out, edgeType, targetObjectRef{
		Group:     group,
		Kind:      kind,
		Namespace: namespace,
		Name:      name,
	})
}

func appendLocalObjectRefList(ctx context.Context, obs core.Observation, out *Evidence, edgeType, kind string, refs any, nameKey string) {
	items, ok := refs.([]any)
	if !ok {
		return
	}
	for _, item := range items {
		ref, ok := item.(map[string]any)
		if !ok {
			continue
		}
		appendNamedRefEdge(ctx, obs, out, edgeType, "", kind, obs.Ref.Namespace, ref, nameKey)
	}
}

func appendEdgeToRef(ctx context.Context, obs core.Observation, out *Evidence, edgeType string, ref targetObjectRef) {
	if edge, ok := edgeToRef(ctx, obs, edgeType, ref); ok {
		out.Edges = append(out.Edges, edge)
	}
}

func specMap(spec map[string]any, key string) map[string]any {
	value, _ := spec[key].(map[string]any)
	return value
}

func objectRefFromMap(ctx context.Context, ref map[string]any, defaultNamespace string) (targetObjectRef, bool) {
	name, _ := ref["name"].(string)
	kind, _ := ref["kind"].(string)
	if name == "" || kind == "" {
		return targetObjectRef{}, false
	}
	namespace, _ := ref["namespace"].(string)
	if namespace == "" && resourceNamespacedForKind(ctx, kind) {
		namespace = defaultNamespace
	}
	apiVersion, _ := ref["apiVersion"].(string)
	group, _ := ref["group"].(string)
	if group == "" {
		group, _ = ref["apiGroup"].(string)
	}
	return targetObjectRef{
		APIVersion: apiVersion,
		Group:      group,
		Kind:       kind,
		Namespace:  namespace,
		Name:       name,
	}, true
}

func mapWithDefaults(ref map[string]any, apiVersion, kind string) map[string]any {
	out := make(map[string]any, len(ref)+2)
	for key, value := range ref {
		out[key] = value
	}
	if _, ok := out["apiVersion"]; !ok && apiVersion != "" {
		out["apiVersion"] = apiVersion
	}
	if _, ok := out["kind"]; !ok && kind != "" {
		out["kind"] = kind
	}
	return out
}

func resourceNamespacedForKind(ctx context.Context, kind string) bool {
	return resolverFromContext(ctx).IsNamespacedKind(kind)
}
