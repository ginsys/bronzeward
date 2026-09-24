-- S2 publication. A release is complete when it holds as many artifacts as it declares; it is
-- stale at commit when a source revision it pinned was superseded by a write ordered before its
-- own 'publish' ledger row.
SELECT 'releases', count(*) FROM release;
SELECT 'artifacts', count(*) FROM release_artifact;
SELECT 'sources', count(*) FROM release_source;
SELECT 'incomplete_releases', count(*) FROM release r WHERE r.artifact_count <> (SELECT count(*) FROM release_artifact a WHERE a.release_id = r.id);
SELECT 'stale_at_commit', count(*) FROM release r JOIN release_source s ON s.release_id = r.id JOIN ledger p ON p.kind = 'publish' AND p.number = r.id WHERE EXISTS (SELECT 1 FROM ledger w WHERE w.kind = 'revision' AND w.subject = s.fragment AND w.number > s.revision AND w.seq < p.seq);
SELECT 'release:' || r.name, r.id || ' ' || r.artifact_count || ' ' || r.digest FROM release r ORDER BY r.name;
