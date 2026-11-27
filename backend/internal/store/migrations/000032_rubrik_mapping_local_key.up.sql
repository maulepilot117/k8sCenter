-- 000032: let the local cluster's Rubrik mapping be keyed "local".
--
-- The Rubrik layer names the local cluster "local" everywhere (the API's
-- X-Cluster-ID, the status, the poller, every child table), but the local
-- cluster's clusters row is keyed by KUBECENTER_CLUSTERID. 000031's foreign
-- key from rubrik_cluster_mappings.cluster_id to clusters(id) therefore
-- refused every local mapping save when that setting was not the default
-- "local".
--
-- The foreign key is replaced by a trigger that keeps its only behaviour
-- that mattered: deleting a cluster deletes its mapping, and the mapping's
-- dependants go by their own cascades. SetMapping still refuses a remote
-- cluster that is not registered, under a lock on its clusters row, so a
-- mapping cannot be written for a cluster being deleted. The local clusters
-- row is never deleted (ClusterStore.Delete excludes is_local).

ALTER TABLE rubrik_cluster_mappings
    DROP CONSTRAINT IF EXISTS rubrik_cluster_mappings_cluster_id_fkey;

CREATE OR REPLACE FUNCTION rubrik_delete_cluster_mapping() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM rubrik_cluster_mappings WHERE cluster_id = OLD.id;
    RETURN OLD;
END $$;

DROP TRIGGER IF EXISTS rubrik_cluster_mapping_cleanup ON clusters;
CREATE TRIGGER rubrik_cluster_mapping_cleanup
    AFTER DELETE ON clusters
    FOR EACH ROW EXECUTE FUNCTION rubrik_delete_cluster_mapping();

COMMENT ON FUNCTION rubrik_delete_cluster_mapping() IS
    'Deletes a cluster''s Rubrik mapping when the cluster is deleted. Replaces the 000031 foreign key, which could not hold the local cluster''s "local" key (000032).';
