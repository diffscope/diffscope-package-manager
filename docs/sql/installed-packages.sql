-- Defines the Spec 2.4 installed package index with schema version 241.
-- The DDL matches packagedatabase/database.go and requires an empty database.
-- Execution does not migrate a database or register package contents.
-- Foreign key enforcement requires configuration on each connection.
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 0;

BEGIN TRANSACTION;

CREATE TABLE packages (
    id TEXT COLLATE BINARY NOT NULL,
    version TEXT NOT NULL,
    compat_version TEXT NOT NULL,
    hash TEXT NOT NULL,
    installed_at INTEGER NOT NULL,
    directory TEXT NOT NULL UNIQUE,
    PRIMARY KEY (id, version)
);

CREATE TABLE dependencies (
    package_id TEXT NOT NULL,
    package_version TEXT NOT NULL,
    id TEXT NOT NULL,
    version TEXT NOT NULL,
    PRIMARY KEY (package_id, package_version, id),
    FOREIGN KEY (package_id, package_version)
        REFERENCES packages (id, version) ON DELETE CASCADE
);

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

CREATE TABLE package_texts (
    package_id TEXT NOT NULL,
    package_version TEXT NOT NULL,
    language TEXT NOT NULL,
    name TEXT,
    description TEXT,
    vendor TEXT,
    PRIMARY KEY (package_id, package_version, language),
    FOREIGN KEY (package_id, package_version)
        REFERENCES packages (id, version) ON DELETE CASCADE
);

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

CREATE TABLE transaction_commits (
    id TEXT PRIMARY KEY NOT NULL
);

PRAGMA user_version = 241;

COMMIT;
