// Package feeds owns CP-side ingestion of threat-intel rule packs and
// IOC lists, refreshing on a schedule and upserting into the existing
// iocs table tagged source = "feed:<slug>".
//
// Fetcher pulls raw bytes (single_file) or maintains a git working
// tree (git). Parsers (in parser/) turn bytes into CatalogEntry.
// Reconcile (in reconcile.go, Task 12) diffs the parsed entries
// against existing feed-scoped iocs rows. The Scheduler (Task 13)
// drives refreshes on a 60s tick. The Worker (Task 14) wires it all
// together for one feed.
package feeds
