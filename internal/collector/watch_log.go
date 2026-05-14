package collector

import "time"

func watchLog(logf WatchLogFunc, message string, args ...any) {
	if logf != nil {
		logf(message, args...)
	}
}

func resourceLabel(resource Resource) string {
	if resource.Name != "" {
		return resource.Name
	}
	if resource.Group == "" {
		return resource.Resource
	}
	if resource.Resource == "" {
		return resource.Group
	}
	return resource.Resource + "." + resource.Group
}

func resourceAPIVersion(resource Resource) string {
	if resource.Version == "" {
		return "<unknown>"
	}
	if resource.Group == "" {
		return resource.Version
	}
	return resource.Group + "/" + resource.Version
}

func watchNamespaceLabel(namespace string) string {
	if namespace == "" {
		return "<all>"
	}
	return namespace
}

func watchTimeoutLabel(timeout time.Duration) string {
	if timeout <= 0 {
		return "until-interrupted"
	}
	return timeout.String()
}

func clusterSource(identity ClusterIdentity) string {
	if identity.Server == "" {
		return identity.Context
	}
	return identity.Context + " " + identity.Server
}

func emptyLabel(value string) string {
	if value == "" {
		return "<empty>"
	}
	return value
}
