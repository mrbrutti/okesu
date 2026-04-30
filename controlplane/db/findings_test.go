package db

import (
	"database/sql"
	"testing"
)

func TestListActiveWarBridgeFindings(t *testing.T) {
	st := openTempStore(t)
	eventID, err := st.InsertEvent(&Event{
		Ts:      1,
		Type:    "finding",
		Agent:   sql.NullString{String: "t", Valid: true},
		RawJSON: "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertFinding(&FindingInsert{
		EventID:  eventID,
		Ts:       1,
		Title:    "EDR critical",
		Severity: "CRITICAL",
		Tags:     "war-bridge",
		Subtype:  "meeting_minutes",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertFinding(&FindingInsert{
		EventID:  eventID,
		Ts:       2,
		Title:    "Routine LOW",
		Severity: "LOW",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := st.ListActiveWarBridgeFindings(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title.String != "EDR critical" {
		t.Errorf("expected only the war-bridge finding; got %+v", got)
	}
}
