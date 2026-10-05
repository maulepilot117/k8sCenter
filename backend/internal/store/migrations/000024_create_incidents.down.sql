-- DESTRUCTIVE. Dropping these tables permanently destroys every captured
-- incident, its evidence, notes and note revisions, and all collaborator
-- grants. There is no export-on-rollback path. Per the master plan's
-- Migration and Rollback section, a binary rollback must NOT require
-- dropping evidence tables. To roll the backend binary back, LEAVE these
-- tables in place and, BEFORE deploying the older image, reset the recorded
-- version:
--     UPDATE schema_migrations SET version = 23, dirty = false;
-- (or `migrate force 23`). Without that reset an image built before 000024
-- fails its startup migration ("no migration found for version 24") and runs
-- with no database. Only run this file when the operator has consciously
-- decided to destroy incident history. See migrations/NOTES.txt (000024).
-- Reverse dependency order: every other table references incidents.
DROP TABLE IF EXISTS incident_grants;
DROP TABLE IF EXISTS incident_note_revisions;
DROP TABLE IF EXISTS incident_notes;
DROP TABLE IF EXISTS incident_evidence;
DROP TABLE IF EXISTS incidents;
