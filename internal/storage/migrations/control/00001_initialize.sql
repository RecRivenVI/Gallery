CREATE TABLE gallery_control_meta (
    key TEXT PRIMARY KEY NOT NULL,
    value TEXT NOT NULL
) STRICT;

INSERT INTO gallery_control_meta (key, value) VALUES
    ('database_role', 'control'),
    ('schema_contract_version', '1');
