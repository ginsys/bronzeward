-- 0008 machine identity (persistence-api.md §3.3, §7.3; execution and recovery choice §10.26): a
-- machine is keyed by its SMBIOS UUID when it reports one and otherwise by its Talos node ID,
-- exactly one of them, fixed at inventory; a cluster by its Talos cluster ID.

-- Bronzeward is unreleased, so this migration supports no earlier database (§11 rule 6): on one
-- holding clusters, PostgreSQL refuses the NOT NULL column and the operator recreates the database.

-- The Talos cluster ID as `talosctl get info` prints it: the standard base64 encoding of 32 bytes,
-- in its one canonical spelling, compared byte for byte.
ALTER TABLE cluster ADD COLUMN talos_cluster_id text NOT NULL
  CHECK (talos_cluster_id ~ '^[A-Za-z0-9+/]{42}[AEIMQUYcgkosw048]=$');
-- §7.3's cluster key: one Talos cluster ID is one record across the installation.
CREATE UNIQUE INDEX cluster_talos_cluster_id ON cluster (talos_cluster_id);

-- SMBIOS's nil and all-ones values are accepted as a UUID (§7.3): each can be recorded once, and a
-- second machine reporting it is recorded by node ID and refused at every comparison.
ALTER TABLE machine DROP CONSTRAINT machine_smbios_uuid_check;
ALTER TABLE machine ALTER COLUMN smbios_uuid DROP NOT NULL;
-- The Talos node ID as `talosctl get identity` prints it: an opaque string of printable ASCII
-- without spaces, compared byte for byte, never normalised.
ALTER TABLE machine ADD COLUMN talos_node_id text CHECK (talos_node_id ~ '^[!-~]{1,128}$');
ALTER TABLE machine ADD CONSTRAINT machine_identity_key CHECK ((smbios_uuid IS NULL) <> (talos_node_id IS NULL));
-- §7.3's other machine key: one Talos node ID is one record across the installation.
CREATE UNIQUE INDEX machine_talos_node_id ON machine (talos_node_id);
