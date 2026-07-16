CREATE TABLE libraries (
    library_id TEXT PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;

CREATE TABLE sources (
    source_id TEXT PRIMARY KEY NOT NULL,
    library_id TEXT NOT NULL REFERENCES libraries(library_id) ON DELETE RESTRICT,
    display_name TEXT NOT NULL,
    root_path TEXT NOT NULL,
    root_key TEXT NOT NULL UNIQUE,
    created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX sources_library_idx ON sources (library_id, source_id);

CREATE TABLE rule_versions (
    semantic_hash TEXT PRIMARY KEY NOT NULL,
    rule_set_id TEXT NOT NULL,
    version TEXT NOT NULL,
    package_hash TEXT NOT NULL UNIQUE,
    canonical_json TEXT NOT NULL,
    compiler_version TEXT NOT NULL,
    rule_ir_hash TEXT NOT NULL,
    compiled_ir_json TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE (rule_set_id, version, package_hash)
) STRICT;

CREATE TABLE source_rule_bindings (
    binding_id TEXT PRIMARY KEY NOT NULL,
    source_id TEXT NOT NULL REFERENCES sources(source_id) ON DELETE RESTRICT,
    semantic_hash TEXT NOT NULL REFERENCES rule_versions(semantic_hash) ON DELETE RESTRICT,
    parameters_json TEXT NOT NULL,
    priority INTEGER NOT NULL,
    rule_ir_hash TEXT NOT NULL,
    compiled_ir_json TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE (source_id, priority)
) STRICT;

CREATE INDEX source_rule_bindings_source_idx ON source_rule_bindings (source_id, priority, binding_id);
