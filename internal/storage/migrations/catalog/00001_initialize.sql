CREATE TABLE gallery_catalog_meta (
    key TEXT PRIMARY KEY NOT NULL,
    value TEXT NOT NULL
) STRICT;

INSERT INTO gallery_catalog_meta (key, value) VALUES
    ('database_role', 'catalog'),
    ('schema_contract_version', '1');
