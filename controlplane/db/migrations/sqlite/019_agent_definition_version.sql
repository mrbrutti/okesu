-- Operator-readable version label for the daimon definition currently
-- loaded by each agent. Set in the daimon file's YAML frontmatter
-- (e.g. `version: 2`); reported via heartbeat alongside the existing
-- definition_hash. The hash is content-derived and opaque; the version
-- is human-set so operators can scan the Agents/Daimons pages and
-- immediately see "host A is on v2, host B is still on v1."
--
-- Optional — empty when the operator hasn't set one in the file.

ALTER TABLE agents ADD COLUMN definition_version TEXT;
