-- Migrates the Spec 2.4 installed index from schema version 240 to 241.
-- Execution requires a verified backup and an exclusive .dspm/lock.
-- The caller must retain the lock through migration and verification.
-- A statement failure requires ROLLBACK and termination without COMMIT.
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 0;
BEGIN IMMEDIATE;

CREATE TEMP TABLE dspm_migration_guard (version INTEGER NOT NULL CHECK (version = 240));
INSERT INTO dspm_migration_guard SELECT user_version FROM pragma_user_version;
DROP TABLE dspm_migration_guard;

ALTER TABLE contribution_names RENAME TO contribution_names_v240;
ALTER TABLE contributions RENAME TO contributions_v240;

CREATE TABLE contributions (
    package_id TEXT NOT NULL,
    package_version TEXT NOT NULL,
    category TEXT NOT NULL,
    id TEXT NOT NULL,
    interface TEXT NOT NULL,
    level TEXT NOT NULL,
    variant TEXT NOT NULL,
    PRIMARY KEY (package_id, package_version, category, id),
    CHECK (category = 'singer' OR (interface = '' AND level = '' AND variant = '')),
    FOREIGN KEY (package_id, package_version)
        REFERENCES packages (id, version) ON DELETE CASCADE
);
INSERT INTO contributions
SELECT package_id, package_version, category, id,
       CASE WHEN category = 'singer' THEN interface ELSE '' END,
       CASE WHEN category = 'singer' THEN level ELSE '' END,
       CASE WHEN category = 'singer' THEN variant ELSE '' END
FROM contributions_v240;

CREATE TABLE contribution_names (
    package_id TEXT NOT NULL,
    package_version TEXT NOT NULL,
    category TEXT NOT NULL,
    contribution_id TEXT NOT NULL,
    language TEXT NOT NULL,
    name TEXT NOT NULL,
    PRIMARY KEY (package_id, package_version, category, contribution_id, language),
    CHECK (category = 'singer'),
    FOREIGN KEY (package_id, package_version, category, contribution_id)
        REFERENCES contributions (package_id, package_version, category, id)
        ON DELETE CASCADE
);
INSERT INTO contribution_names
SELECT * FROM contribution_names_v240 WHERE category = 'singer';

DROP TABLE contribution_names_v240;
DROP TABLE contributions_v240;
PRAGMA user_version = 241;
COMMIT;
