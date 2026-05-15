create table if not exists clusters (
  id integer primary key,
  name text not null unique,
  uid text,
  source text,
  created_at integer not null
);

create table if not exists api_resources (
  id integer primary key,
  api_group text not null,
  api_version text not null,
  resource text not null,
  kind text not null,
  namespaced boolean not null,
  preferred_version text,
  storage_version text,
  verbs text not null,
  last_discovered_at integer,
  removed_at integer,
  unique(api_group, api_version, resource)
);

create table if not exists object_kinds (
  id integer primary key,
  api_resource_id integer not null references api_resources(id),
  api_group text not null,
  api_version text not null,
  kind text not null,
  unique(api_group, api_version, kind)
);

create table if not exists resource_processing_profiles (
  api_resource_id integer primary key references api_resources(id),
  profile text not null,
  retention_class text not null,
  filter_chain text not null,
  extractor_set text not null,
  compaction_strategy text not null,
  priority text not null,
  max_event_buffer integer not null,
  enabled boolean not null
);

create table if not exists objects (
  id integer primary key,
  cluster_id integer not null references clusters(id),
  kind_id integer not null references object_kinds(id),
  namespace text,
  name text not null,
  uid text,
  latest_version_id integer,
  first_seen_at integer not null,
  last_seen_at integer not null,
  deleted_at integer
);

create table if not exists blobs (
  digest text primary key,
  codec text not null,
  raw_size integer not null,
  stored_size integer not null,
  data blob not null
);

create table if not exists versions (
  id integer primary key,
  object_id integer not null references objects(id),
  seq integer not null,
  observed_at integer not null,
  resource_version text,
  generation integer,
  doc_hash text not null,
  materialization text not null,
  strategy text not null,
  blob_ref text not null references blobs(digest),
  parent_version_id integer,
  raw_size integer not null,
  stored_size integer not null,
  replay_depth integer not null,
  summary text,
  unique(object_id, seq)
);

create table if not exists object_observations (
  id integer primary key,
  cluster_id integer not null references clusters(id),
  object_id integer not null references objects(id),
  observed_at integer not null,
  observation_type text not null,
  resource_version text,
  version_id integer references versions(id),
  content_changed boolean not null
);

create table if not exists latest_index (
  object_id integer primary key references objects(id),
  cluster_id integer not null references clusters(id),
  kind_id integer not null references object_kinds(id),
  namespace text,
  name text not null,
  uid text,
  latest_version_id integer not null,
  observed_at integer not null
);

create table if not exists latest_raw_index (
  object_id integer primary key references objects(id),
  cluster_id integer not null references clusters(id),
  kind_id integer not null references object_kinds(id),
  namespace text,
  name text not null,
  uid text,
  observed_at integer not null,
  observation_type text not null,
  resource_version text,
  generation integer,
  doc_hash text not null,
  raw_size integer not null,
  doc blob not null
);

create view if not exists latest_documents as
select
  li.object_id,
  li.cluster_id,
  li.kind_id,
  li.namespace,
  li.name,
  li.uid,
  li.latest_version_id,
  li.observed_at,
  cast(b.data as text) as doc
from latest_index li
join versions v on v.id = li.latest_version_id
join blobs b on b.digest = v.blob_ref
where b.codec = 'identity';

create view if not exists latest_raw_documents as
select
  object_id,
  cluster_id,
  kind_id,
  namespace,
  name,
  uid,
  observed_at,
  observation_type,
  resource_version,
  generation,
  doc_hash,
  raw_size,
  cast(doc as text) as doc
from latest_raw_index;

create table if not exists object_edges (
  id integer primary key,
  cluster_id integer not null references clusters(id),
  edge_type text not null,
  src_id integer not null references objects(id),
  dst_id integer not null references objects(id),
  valid_from integer not null,
  valid_to integer not null,
  src_version_id integer,
  dst_version_id integer,
  confidence integer not null default 100,
  detail text
);

create table if not exists object_facts (
  id integer primary key,
  cluster_id integer not null references clusters(id),
  ts integer not null,
  object_id integer not null references objects(id),
  version_id integer,
  kind_id integer not null references object_kinds(id),
  namespace text,
  name text,
  node_id integer,
  workload_id integer,
  service_id integer,
  fact_key text not null,
  fact_value text,
  numeric_value real,
  severity integer not null default 0,
  detail text
);

create table if not exists object_changes (
  id integer primary key,
  cluster_id integer not null references clusters(id),
  ts integer not null,
  object_id integer not null references objects(id),
  version_id integer,
  change_family text not null,
  path text not null,
  op text not null,
  old_scalar text,
  new_scalar text,
  severity integer not null default 0
);

create table if not exists filter_decisions (
  id integer primary key,
  ts integer not null,
  cluster_name text not null,
  api_group text not null,
  api_version text not null,
  resource text not null,
  kind text not null,
  namespace text,
  name text not null,
  uid text,
  resource_version text,
  observation_type text not null,
  filter_name text not null,
  outcome text not null,
  reason text not null,
  destructive boolean not null,
  meta text
);

create table if not exists ingestion_offsets (
  cluster_id integer not null references clusters(id),
  api_resource_id integer not null references api_resources(id),
  namespace text not null,
  resource_version text,
  last_list_at integer,
  last_watch_at integer,
  last_bookmark_at integer,
  status text not null,
  error text,
  updated_at integer not null,
  primary key(cluster_id, api_resource_id, namespace)
);

create table if not exists maintenance_runs (
  id integer primary key,
  cluster_id integer references clusters(id),
  backend text not null,
  task text not null,
  started_at integer not null,
  finished_at integer,
  status text not null,
  rows_scanned integer,
  rows_changed integer,
  bytes_before integer,
  bytes_after integer,
  error text
);

create index if not exists latest_kind_ns_name_idx
on latest_index(cluster_id, kind_id, namespace, name);

create index if not exists latest_raw_kind_ns_name_idx
on latest_raw_index(cluster_id, kind_id, namespace, name);

create index if not exists versions_object_seq_idx
on versions(object_id, seq desc);

create index if not exists versions_object_time_idx
on versions(object_id, observed_at desc);

create index if not exists object_observations_object_time_idx
on object_observations(object_id, observed_at desc);

create index if not exists object_observations_cluster_time_idx
on object_observations(cluster_id, observed_at desc);

create index if not exists object_edges_src_time_idx
on object_edges(cluster_id, edge_type, src_id, valid_from, valid_to);

create index if not exists object_edges_dst_time_idx
on object_edges(cluster_id, edge_type, dst_id, valid_from, valid_to);

create index if not exists object_facts_key_value_time_idx
on object_facts(cluster_id, fact_key, fact_value, ts desc);

create index if not exists object_facts_object_time_idx
on object_facts(cluster_id, object_id, ts desc);

create index if not exists object_facts_workload_time_idx
on object_facts(cluster_id, workload_id, ts desc);

create index if not exists object_facts_service_time_idx
on object_facts(cluster_id, service_id, ts desc);

create index if not exists object_facts_node_time_idx
on object_facts(cluster_id, node_id, ts desc);

create index if not exists object_changes_object_time_idx
on object_changes(cluster_id, object_id, ts desc);

create index if not exists object_changes_path_time_idx
on object_changes(cluster_id, change_family, path, ts desc);

create index if not exists filter_decisions_ref_time_idx
on filter_decisions(cluster_name, resource, namespace, name, ts desc);

create index if not exists filter_decisions_outcome_time_idx
on filter_decisions(outcome, ts desc);
