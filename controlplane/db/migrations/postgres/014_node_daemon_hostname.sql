-- The hostname an operator registers a node with (10.0.1.42, my-node.local)
-- often differs from what the daemon's `os.Hostname()` reports inside the
-- guest — VMs, containers, and Lima boxes routinely have an internal
-- hostname unrelated to the SSH target. Daemon events are tagged with the
-- internal hostname, so filtering NodeDetail's Live Events by
-- `nodes.hostname` produced empty feeds.
--
-- Capture the daemon-side hostname at deploy time (single SSH `hostname`
-- call) and persist it here. NodeDetail prefers this over the registered
-- hostname when filtering, falling back to the registered hostname for
-- nodes that haven't been redeployed since this column was added.

ALTER TABLE nodes ADD COLUMN daemon_hostname TEXT;
