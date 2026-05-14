package core

import "time"

type ResourceRef struct {
	ClusterID string
	Group     string
	Version   string
	Resource  string
	Kind      string
	Namespace string
	Name      string
	UID       string
}

func (r ResourceRef) HumanKey() string {
	if r.Namespace == "" {
		return r.Group + "/" + r.Resource + "/" + r.Name
	}
	return r.Group + "/" + r.Resource + "/" + r.Namespace + "/" + r.Name
}

type ObservationType string

const (
	ObservationAdded    ObservationType = "ADDED"
	ObservationModified ObservationType = "MODIFIED"
	ObservationDeleted  ObservationType = "DELETED"
	ObservationBookmark ObservationType = "BOOKMARK"
)

type Observation struct {
	Type            ObservationType
	ObservedAt      time.Time
	ResourceVersion string
	Ref             ResourceRef
	Object          map[string]any
	Partial         bool
}

type VersionMaterialization string

const (
	MaterializationFull         VersionMaterialization = "full"
	MaterializationReverseDelta VersionMaterialization = "reverse_delta"
)

type StoredVersion struct {
	ObjectID        string
	Sequence        int64
	ObservedAt      time.Time
	ResourceVersion string
	DocumentHash    string
	Materialization VersionMaterialization
	BlobRef         string
	Summary         map[string]any
}

type Fact struct {
	Time         time.Time
	ObjectID     string
	Key          string
	Value        string
	NumericValue *float64
	Severity     int
	Detail       map[string]any
}

type Edge struct {
	Type      string
	SourceID  string
	TargetID  string
	ValidFrom time.Time
	ValidTo   *time.Time
	Detail    map[string]any
}

type Change struct {
	Time     time.Time
	ObjectID string
	Family   string
	Path     string
	Op       string
	Old      string
	New      string
	Severity int
}
