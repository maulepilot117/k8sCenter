-- Reverses 000032: restores 000031's foreign key from
-- rubrik_cluster_mappings.cluster_id to clusters(id).
--
-- A mapping keyed by an id with no clusters row (the local cluster's
-- "local" mapping, when KUBECENTER_CLUSTERID is not "local") cannot satisfy
-- the foreign key, so it is deleted first, with its poller state,
-- compliance states, events and deliveries (their cascades). An admin maps
-- that cluster again after a roll-forward; under the old schema the save
-- is refused, as it was before 000032.

DROP TRIGGER IF EXISTS rubrik_cluster_mapping_cleanup ON clusters;
DROP FUNCTION IF EXISTS rubrik_delete_cluster_mapping();

DELETE FROM rubrik_cluster_mappings m
WHERE NOT EXISTS (SELECT 1 FROM clusters c WHERE c.id = m.cluster_id);

ALTER TABLE rubrik_cluster_mappings
    DROP CONSTRAINT IF EXISTS rubrik_cluster_mappings_cluster_id_fkey;
ALTER TABLE rubrik_cluster_mappings
    ADD CONSTRAINT rubrik_cluster_mappings_cluster_id_fkey
    FOREIGN KEY (cluster_id) REFERENCES clusters(id) ON DELETE CASCADE;
