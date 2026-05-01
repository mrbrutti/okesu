package feeds

import (
	"fmt"

	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/ioc/catalog"
)

// ReconcileResult summarizes one reconcile pass: counts of rows
// inserted (newly created), updated (existed and were re-upserted),
// and deleted (existed in feed scope before but not in `entries`).
type ReconcileResult struct {
	Inserted int
	Updated  int
	Deleted  int
}

// Reconcile makes the iocs rows scoped to feedID match `entries`
// exactly. The source on every row is set to "feed:<slug>". Rows that
// disappear are deleted; their observations get
// orphaned_rule_label = "<slug>:<name>" via DeleteIOCsByFeed.
//
// Used by the worker on every refresh, and by uninstall (which calls
// Reconcile with `entries == nil` for reconcile-to-empty semantics).
func Reconcile(store *db.Store, feedID int64, slug string, entries []catalog.CatalogEntry) (*ReconcileResult, error) {
	existing, err := store.ListIOCsByFeed(feedID)
	if err != nil {
		return nil, fmt.Errorf("list existing: %w", err)
	}

	type key struct{ Kind, Norm string }
	existingByKey := map[key]int64{}
	for _, r := range existing {
		existingByKey[key{r.Kind, r.NormalizedValue}] = r.ID
	}

	res := &ReconcileResult{}
	keepIDs := map[int64]bool{}
	for _, e := range entries {
		k := key{e.Kind, e.NormalizedValue}
		feedRef := feedID
		_, created, err := store.UpsertIOC(&db.IOCUpsert{
			Kind:            e.Kind,
			Value:           e.Value,
			NormalizedValue: e.NormalizedValue,
			Source:          "feed:" + slug,
			Confidence:      e.Confidence,
			Attribution:     e.Attribution,
			SeverityFloor:   e.SeverityFloor,
			Classification:  e.Classification,
			Notes:           e.Notes,
			Name:            e.Name,
			Tags:            e.Tags,
			FeedID:          &feedRef,
		})
		if err != nil {
			return nil, fmt.Errorf("upsert: %w", err)
		}
		if created {
			res.Inserted++
		} else {
			res.Updated++
		}
		if id, ok := existingByKey[k]; ok {
			keepIDs[id] = true
		}
	}

	var toDelete []int64
	for _, id := range existingByKey {
		if !keepIDs[id] {
			toDelete = append(toDelete, id)
		}
	}
	if err := store.DeleteIOCsByFeed(feedID, slug+":", toDelete); err != nil {
		return nil, fmt.Errorf("delete orphans: %w", err)
	}
	res.Deleted = len(toDelete)
	return res, nil
}
