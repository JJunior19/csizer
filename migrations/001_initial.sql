CREATE TABLE workloads (
    id INTEGER PRIMARY KEY,
    workload_key TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    compose_project TEXT,
    compose_service TEXT,
    image_repository TEXT,
    tracking_enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE container_instances (
    id INTEGER PRIMARY KEY,
    workload_id INTEGER NOT NULL REFERENCES workloads(id),
    container_id TEXT NOT NULL UNIQUE,
    container_name TEXT NOT NULL,
    image_name TEXT NOT NULL,
    image_digest TEXT,
    architecture TEXT,
    operating_system TEXT,
    docker_host_id TEXT,
    started_at TEXT,
    stopped_at TEXT,
    exit_code INTEGER,
    oom_killed INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL
);

CREATE TABLE tracking_sessions (
    id INTEGER PRIMARY KEY,
    workload_id INTEGER NOT NULL REFERENCES workloads(id),
    container_instance_id INTEGER REFERENCES container_instances(id),
    started_at TEXT NOT NULL,
    ended_at TEXT,
    status TEXT NOT NULL,
    label TEXT,
    collector_version TEXT
);

CREATE TABLE metric_samples (
    id INTEGER PRIMARY KEY,
    session_id INTEGER NOT NULL REFERENCES tracking_sessions(id),
    timestamp TEXT NOT NULL,
    cpu_usage_cores REAL NOT NULL,
    cpu_percent_host REAL,
    memory_usage_bytes INTEGER NOT NULL,
    memory_cache_bytes INTEGER,
    memory_working_set_bytes INTEGER NOT NULL,
    memory_limit_bytes INTEGER,
    pids INTEGER,
    network_rx_bytes INTEGER,
    network_tx_bytes INTEGER,
    block_read_bytes INTEGER,
    block_write_bytes INTEGER,
    activity_state TEXT NOT NULL
);

CREATE TABLE container_events (
    id INTEGER PRIMARY KEY,
    workload_id INTEGER NOT NULL REFERENCES workloads(id),
    container_instance_id INTEGER REFERENCES container_instances(id),
    timestamp TEXT NOT NULL,
    event_type TEXT NOT NULL,
    exit_code INTEGER,
    metadata_json TEXT
);

CREATE TABLE minute_rollups (
    workload_id INTEGER NOT NULL REFERENCES workloads(id),
    minute TEXT NOT NULL,
    sample_count INTEGER NOT NULL,
    active_sample_count INTEGER NOT NULL,
    cpu_avg REAL,
    cpu_p95 REAL,
    cpu_p99 REAL,
    cpu_max REAL,
    memory_avg_bytes INTEGER,
    memory_p95_bytes INTEGER,
    memory_p99_bytes INTEGER,
    memory_max_bytes INTEGER,
    PRIMARY KEY (workload_id, minute)
);

CREATE TABLE workload_settings (
    workload_id INTEGER PRIMARY KEY REFERENCES workloads(id),
    active_sample_interval_seconds INTEGER NOT NULL DEFAULT 2,
    idle_sample_interval_seconds INTEGER NOT NULL DEFAULT 10,
    raw_retention_days INTEGER NOT NULL DEFAULT 30,
    startup_window_seconds INTEGER NOT NULL DEFAULT 20,
    default_provider TEXT,
    default_profile TEXT NOT NULL DEFAULT 'balanced'
);

CREATE INDEX idx_container_instances_workload_id
    ON container_instances(workload_id);
CREATE INDEX idx_tracking_sessions_workload_started_at
    ON tracking_sessions(workload_id, started_at);
CREATE INDEX idx_tracking_sessions_container_instance_id
    ON tracking_sessions(container_instance_id);
CREATE INDEX idx_metric_samples_session_timestamp
    ON metric_samples(session_id, timestamp);
CREATE INDEX idx_metric_samples_timestamp
    ON metric_samples(timestamp);
CREATE INDEX idx_container_events_workload_timestamp
    ON container_events(workload_id, timestamp);
CREATE INDEX idx_container_events_container_instance_timestamp
    ON container_events(container_instance_id, timestamp);
