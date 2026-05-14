package sanitize

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestKubernetesJSONRedactsSensitiveFields(t *testing.T) {
	input := []byte(`{
	  "apiVersion": "v1",
	  "kind": "Secret",
	  "metadata": {
	    "name": "prod-db",
	    "namespace": "payments",
	    "uid": "abc-123",
	    "managedFields": [{"manager": "kubectl"}],
	    "annotations": {
	      "kubectl.kubernetes.io/last-applied-configuration": "raw",
	      "company.example/token": "secret-token"
	    }
	  },
	  "data": {"password": "cGFzcw=="},
	  "stringData": {"token": "plain"}
	}`)

	out, err := KubernetesJSON(input, KubernetesOptions{Salt: "test"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, leaked := range []string{"prod-db", "payments", "abc-123", "cGFzcw==", "plain", "managedFields", "last-applied"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("sanitized output leaked %q: %s", leaked, text)
		}
	}
	if !strings.Contains(text, `"password"`) || !strings.Contains(text, `"token"`) {
		t.Fatalf("sanitized output did not keep secret keys: %s", text)
	}
	if !strings.Contains(text, "fake-secret-") {
		t.Fatalf("sanitized output did not generate fake secret values: %s", text)
	}
}

func TestKubernetesJSONKeepsListShape(t *testing.T) {
	input := []byte(`{
	  "apiVersion": "v1",
	  "kind": "List",
	  "items": [{
	    "apiVersion": "v1",
	    "kind": "Pod",
	    "metadata": {"name": "api", "namespace": "default", "uid": "pod-uid"},
	    "spec": {"nodeName": "node-a", "containers": [{"name": "app", "image": "registry.local/app:v1"}]},
	    "status": {"podIP": "10.1.2.3", "phase": "Running"}
	  }]
	}`)

	out, err := KubernetesJSON(input, KubernetesOptions{Salt: "test"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatal(err)
	}
	items := decoded["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	text := string(out)
	for _, leaked := range []string{"node-a", "registry.local/app", "10.1.2.3", "default"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("sanitized output leaked %q: %s", leaked, text)
		}
	}
}

func TestKubernetesJSONRedactsGenerateNameAndUnsafeImageTags(t *testing.T) {
	input := []byte(`{
	  "apiVersion": "v1",
	  "kind": "Pod",
	  "metadata": {"name": "api-1", "generateName": "payments-api-", "namespace": "default", "uid": "pod-uid"},
	  "spec": {"containers": [{"name": "app", "image": "registry.local/payments-api:payments-api-staging"}]}
	}`)

	out, err := KubernetesJSON(input, KubernetesOptions{Salt: "test"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, leaked := range []string{"payments-api", "registry.local"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("sanitized output leaked %q: %s", leaked, text)
		}
	}
	if !strings.Contains(text, "tag-") {
		t.Fatalf("sanitized output did not hash unsafe image tag: %s", text)
	}
}

func TestKubernetesJSONRedactsSelectorValues(t *testing.T) {
	input := []byte(`{
	  "apiVersion": "v1",
	  "kind": "Service",
	  "metadata": {"name": "api", "namespace": "default", "uid": "svc-uid"},
	  "spec": {
	    "selector": {"app.kubernetes.io/name": "payments-api"},
	    "template": {
	      "spec": {
	        "affinity": {
	          "podAntiAffinity": {
	            "preferredDuringSchedulingIgnoredDuringExecution": [{
	              "podAffinityTerm": {
	                "labelSelector": {
	                  "matchExpressions": [{
	                    "key": "app.kubernetes.io/name",
	                    "operator": "In",
	                    "values": ["payments-api"]
	                  }]
	                }
	              }
	            }]
	          }
	        }
	      }
	    }
	  }
	}`)

	out, err := KubernetesJSON(input, KubernetesOptions{Salt: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "payments-api") {
		t.Fatalf("sanitized output leaked selector value: %s", out)
	}
}

func TestKubernetesJSONPreservesCRDSchemaText(t *testing.T) {
	input := []byte(`{
	  "apiVersion": "apiextensions.k8s.io/v1",
	  "kind": "CustomResourceDefinition",
	  "metadata": {"name": "widgets.example.com", "uid": "crd-uid"},
	  "spec": {
	    "group": "example.com",
	    "names": {"kind": "Widget", "plural": "widgets"},
	    "versions": [{
	      "name": "v1",
	      "schema": {
	        "openAPIV3Schema": {
	          "description": "Widget configures an example controller.",
	          "properties": {
	            "spec": {
	              "properties": {
	                "token": {"type": "string"},
	                "replicas": {"description": "Number of replicas.", "type": "integer"}
	              }
	            }
	          }
	        }
	      }
	    }],
	    "conversion": {
	      "webhook": {
	        "clientConfig": {
	          "caBundle": "real-ca",
	          "url": "https://internal.example.com/hook"
	        }
	      }
	    }
	  }
	}`)

	out, err := KubernetesJSON(input, KubernetesOptions{Salt: "test"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, want := range []string{"widgets.example.com", "Widget configures an example controller.", "Number of replicas."} {
		if !strings.Contains(text, want) {
			t.Fatalf("sanitized CRD did not preserve %q: %s", want, text)
		}
	}
	for _, leaked := range []string{"real-ca", "internal.example.com"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("sanitized CRD leaked %q: %s", leaked, text)
		}
	}
}
