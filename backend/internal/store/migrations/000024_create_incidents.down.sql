-- DESTRUCTIVE. Dropping these tables permanently destroys every captured
-- incident, its evidence, notes and note revisions, and all collaborator
-- grants. There is no export-on-rollback path. Per the master plan's
-- Migration and Rollback section, a binary rollback must NOT require
-- dropping evidence tables: to roll the backend binary back, deploy the
-- older image and LEAVE these tables in place. Only run this file when the
-- operator has consciously decided to destroy incident history. See
-- migrations/NOTES.txt (000024) before running it.
-- Reverse dependency order: every other table references incidents.
DROP TABLE IF EXISTS incident_grants;
DROP TABLE IF EXISTS incident_note_revisions;
DROP TABLE IF EXISTS incident_notes;
DROP TABLE IF EXISTS incident_evidence;
DROP TABLE IF EXISTS incidents;
