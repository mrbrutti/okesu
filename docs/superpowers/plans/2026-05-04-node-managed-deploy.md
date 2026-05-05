# Node Managed Deploy (OCI) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a third "Managed deploy" tab to Add Node that provisions an OCI VM, drops the existing fleet enrollment package onto it via cloud-init, and runs the okesu daemon as a systemd service. The new node auto-registers via S3 with whichever CP owns the picked transport_config.

**Architecture:** Mirror the cp-provision flow we just shipped (PR #129). New `node_provisions` table parallel to `cp_provisions`. Reuse the cross-cloud `cpprovision.Provisioner` interface (it's already abstract — Launch/Destroy/Estimate don't care what payload runs in user_data). Reuse `controlplane/packaging` to build the node tarball. The `node_provision_worker` uploads the package to S3, hands cloud-init a presigned GET URL, and lets the existing `install.sh` install + start a systemd unit. Status flips to `ready` when the s3scanner first records a node row whose `provision_id` matches.

**Tech Stack:** Go (controlplane), SQLite + Postgres dual-dialect migrations, React/TypeScript frontend, OCI Compute SDK, minio-go, systemd.

**v1 scope (this plan):** Operator picks an existing local `transport_configs.id`. Whichever CP scans that bucket prefix gets the node — exactly the same trust model as the SSH/manual S3 tabs. Cross-CP transport_config sharing (parent UI deploys a node into a federated child CP's bucket without admin pre-config) is **v2** and out of scope here.

**Cleanup follow-ups bundled in this plan (per #129's TODO list):**
- Destroying a `cp_provisions` row also deletes its pre-registered `federation_peers` row.
- Destroying a `node_provisions` row also deletes its `nodes` row when the node has registered.
- One-time bundle blobs in `cp/<id>/bootstrap/bundle.tar.gz` are deleted after first successful introspect.

---

## File Structure

**New files (controlplane):**
- `controlplane/db/migrations/sqlite/060_node_provisions.sql` — `node_provisions` table.
- `controlplane/db/migrations/postgres/060_node_provisions.sql` — same, postgres dialect.
- `controlplane/db/node_provisions.go` — Store CRUD + status-advance helpers (mirrors `db/cp_provisions.go`).
- `controlplane/api/node_provision.go` — POST + DELETE handlers (mirrors `api/cp_provision.go`).
- `controlplane/api/node_provision_list.go` — GET list/get handlers.
- `controlplane/api/node_provision_worker.go` — worker goroutine (mirrors `api/cp_provision_worker.go`).
- `controlplane/api/node_provision_estimate.go` — cost-estimate handler (reuses `cpprovision/costcatalog`).
- `controlplane/api/node_provision_test.go`, `node_provision_worker_test.go` — table-driven tests.

**New files (web):**
- `web/src/components/nodes/AddNodeManaged.tsx` — third tab body (mirrors `Federation.tsx::ManagedDeployPanel`).
- `web/src/components/nodes/NodeProvisionsPanel.tsx` — in-flight + recent provisions table on the Nodes page.

**Modified files (controlplane):**
- `controlplane/api/cp_provision.go` — destroy handler also deletes the linked `federation_peers` row + bucket bundle blob.
- `controlplane/api/cp_bundle_s3deaddrop.go` — small extension: a hook called from `s3reader` after first successful poll deletes the bootstrap blob.
- `controlplane/federation/s3reader/reader.go` — call the bundle-cleanup hook on first success.
- `controlplane/server.go` — wire the four new endpoints into the chi router.
- `controlplane/packaging/packaging.go` — extend `installShellScript` to write a systemd unit when `OKESU_INSTALL_AS_SERVICE=1` is set in the env, instead of nohup.

**Modified files (web):**
- `web/src/api.ts` — `nodeProvisionsList`, `nodeProvision`, `nodeProvisionCreate`, `nodeProvisionDelete`, `nodeProvisionEstimate` client methods + types.
- `web/src/pages/Nodes.tsx` — add "Managed deploy" tab to `AddNodeModal`; add `<NodeProvisionsPanel />` below the node list.

---

## Task 1 — Database migration

**Files:**
- Create: `controlplane/db/migrations/sqlite/060_node_provisions.sql`
- Create: `controlplane/db/migrations/postgres/060_node_provisions.sql`

- [ ] **Step 1: Write the SQLite migration**

```sql
-- 060_node_provisions.sql
-- Tracks a managed-deploy provisioning run: a CP-issued cloud VM
-- launch that auto-installs the okesu daemon and self-registers
-- via S3 against the picked transport_config. Mirrors cp_provisions
-- in shape; node_id (instead of peer_id) links to the nodes row
-- once registration completes.
CREATE TABLE node_provisions (
  id                      INTEGER PRIMARY KEY AUTOINCREMENT,
  display_name            TEXT NOT NULL,
  region                  TEXT NOT NULL,
  cloud                   TEXT NOT NULL,
  credential_id           INTEGER REFERENCES cloud_credentials(id) ON DELETE SET NULL,
  credential_name         TEXT,
  cloud_params_json       TEXT NOT NULL DEFAULT '{}',
  transport_config_id     INTEGER NOT NULL REFERENCES transport_configs(id) ON DELETE RESTRICT,
  status                  TEXT NOT NULL DEFAULT 'queued',
  cloud_resource_id       TEXT,
  cloud_resource_url      TEXT,
  node_id                 INTEGER REFERENCES nodes(id) ON DELETE SET NULL,
  log                     TEXT NOT NULL DEFAULT '',
  error                   TEXT,
  est_cost_per_hour_usd   REAL,
  instance_shape          TEXT,
  created_at              TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  started_at              TIMESTAMP,
  ended_at                TIMESTAMP,
  created_by_user_id      INTEGER REFERENCES users(id) ON DELETE SET NULL,
  created_by_email        TEXT
);
CREATE INDEX idx_node_provisions_status ON node_provisions(status, created_at DESC);
CREATE INDEX idx_node_provisions_cloud  ON node_provisions(cloud, created_at DESC);
```

- [ ] **Step 2: Write the Postgres migration (same DDL, with SERIAL/BIGSERIAL adjustments)**

```sql
-- 060_node_provisions.sql (postgres)
CREATE TABLE node_provisions (
  id                      BIGSERIAL PRIMARY KEY,
  display_name            TEXT NOT NULL,
  region                  TEXT NOT NULL,
  cloud                   TEXT NOT NULL,
  credential_id           BIGINT REFERENCES cloud_credentials(id) ON DELETE SET NULL,
  credential_name         TEXT,
  cloud_params_json       TEXT NOT NULL DEFAULT '{}',
  transport_config_id     BIGINT NOT NULL REFERENCES transport_configs(id) ON DELETE RESTRICT,
  status                  TEXT NOT NULL DEFAULT 'queued',
  cloud_resource_id       TEXT,
  cloud_resource_url      TEXT,
  node_id                 BIGINT REFERENCES nodes(id) ON DELETE SET NULL,
  log                     TEXT NOT NULL DEFAULT '',
  error                   TEXT,
  est_cost_per_hour_usd   DOUBLE PRECISION,
  instance_shape          TEXT,
  created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
  started_at              TIMESTAMPTZ,
  ended_at                TIMESTAMPTZ,
  created_by_user_id      BIGINT REFERENCES users(id) ON DELETE SET NULL,
  created_by_email        TEXT
);
CREATE INDEX idx_node_provisions_status ON node_provisions(status, created_at DESC);
CREATE INDEX idx_node_provisions_cloud  ON node_provisions(cloud, created_at DESC);
```

- [ ] **Step 3: Verify migrations apply**

Run: `make cp && rm -f /tmp/test-cp.db && ./okesu-cp serve --db /tmp/test-cp.db --listen :17443 --mgmt-listen :17444 --admin-password test &`
Then: `sqlite3 /tmp/test-cp.db ".schema node_provisions"`
Expected: schema printed including all columns.
Kill the test CP after.

- [ ] **Step 4: Commit**

```bash
git add controlplane/db/migrations/sqlite/060_node_provisions.sql controlplane/db/migrations/postgres/060_node_provisions.sql
git commit -m "feat(db): node_provisions table — schema mirroring cp_provisions"
```

---

## Task 2 — Store CRUD + advance helpers

**Files:**
- Create: `controlplane/db/node_provisions.go`
- Test: `controlplane/db/node_provisions_test.go`

- [ ] **Step 1: Write the failing test (CRUD + AdvanceByNode)**

```go
// controlplane/db/node_provisions_test.go
package db

import (
	"database/sql"
	"testing"
)

func TestNodeProvisionCRUD(t *testing.T) {
	st := newTestStore(t)
	tcID, _ := st.CreateTransportConfig(TransportConfig{
		Name: "t", Kind: "s3", Bucket: "b", Endpoint: "h",
	})
	r, err := st.InsertNodeProvision(NodeProvisionInsert{
		DisplayName: "n1", Region: "us-phoenix-1", Cloud: "oci",
		CloudParamsJSON: `{"shape":"VM.Standard.E4.Flex"}`,
		TransportConfigID: tcID,
	})
	if err != nil { t.Fatal(err) }
	if r.Status != NodeProvisionQueued {
		t.Errorf("status=%q want queued", r.Status)
	}

	// status flip via node id
	if err := st.SetNodeProvisionNode(r.ID, 99); err != nil { t.Fatal(err) }
	advanced, err := st.AdvanceNodeProvisionByNode(99)
	if err != nil || !advanced {
		t.Errorf("advanced=%v err=%v", advanced, err)
	}
	row, _ := st.NodeProvision(r.ID)
	if row.Status != NodeProvisionReady {
		t.Errorf("status=%q want ready", row.Status)
	}
	// idempotent
	advanced, _ = st.AdvanceNodeProvisionByNode(99)
	if advanced { t.Error("second call advanced") }
}

var _ = sql.NullString{}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./controlplane/db/ -run TestNodeProvisionCRUD -v`
Expected: compile error — types don't exist.

- [ ] **Step 3: Implement the store**

```go
// controlplane/db/node_provisions.go
package db

import (
	"database/sql"
	"fmt"
	"time"
)

type NodeProvisionStatus string

const (
	NodeProvisionQueued           NodeProvisionStatus = "queued"
	NodeProvisionStarting         NodeProvisionStatus = "starting"
	NodeProvisionCloudInitRunning NodeProvisionStatus = "cloud_init_running"
	NodeProvisionBootstrapPending NodeProvisionStatus = "bootstrap_pending"
	NodeProvisionReady            NodeProvisionStatus = "ready"
	NodeProvisionFailed           NodeProvisionStatus = "failed"
)

type NodeProvision struct {
	ID                int64
	DisplayName       string
	Region            string
	Cloud             string
	CredentialID      sql.NullInt64
	CredentialName    sql.NullString
	CloudParamsJSON   string
	TransportConfigID int64
	Status            NodeProvisionStatus
	CloudResourceID   sql.NullString
	CloudResourceURL  sql.NullString
	NodeID            sql.NullInt64
	Log               string
	Error             sql.NullString
	EstCostPerHourUSD sql.NullFloat64
	InstanceShape     sql.NullString
	CreatedAt         time.Time
	StartedAt         sql.NullTime
	EndedAt           sql.NullTime
	CreatedByUserID   sql.NullInt64
	CreatedByEmail    sql.NullString
}

type NodeProvisionInsert struct {
	DisplayName       string
	Region            string
	Cloud             string
	CredentialID      sql.NullInt64
	CredentialName    string
	CloudParamsJSON   string
	TransportConfigID int64
	EstCostPerHourUSD sql.NullFloat64
	InstanceShape     string
	CreatedByUserID   sql.NullInt64
	CreatedByEmail    string
}

func (s *Store) InsertNodeProvision(in NodeProvisionInsert) (*NodeProvision, error) {
	res, err := s.Exec(`
		INSERT INTO node_provisions
		  (display_name, region, cloud, credential_id, credential_name,
		   cloud_params_json, transport_config_id,
		   est_cost_per_hour_usd, instance_shape,
		   created_by_user_id, created_by_email)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		in.DisplayName, in.Region, in.Cloud, in.CredentialID, nullable(in.CredentialName),
		in.CloudParamsJSON, in.TransportConfigID,
		in.EstCostPerHourUSD, nullable(in.InstanceShape),
		in.CreatedByUserID, nullable(in.CreatedByEmail))
	if err != nil { return nil, err }
	id, _ := res.LastInsertId()
	return s.NodeProvision(id)
}

func (s *Store) NodeProvision(id int64) (*NodeProvision, error) {
	row := s.QueryRow(nodeProvisionSelect+` WHERE id = ?`, id)
	return scanNodeProvision(row)
}

func (s *Store) ListNodeProvisions(limit int) ([]NodeProvision, error) {
	if limit <= 0 || limit > 200 { limit = 50 }
	rows, err := s.Query(nodeProvisionSelect+` ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []NodeProvision
	for rows.Next() {
		r, err := scanNodeProvision(rows)
		if err != nil { return nil, err }
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *Store) UpdateNodeProvisionStatus(id int64, st NodeProvisionStatus) error {
	_, err := s.Exec(`UPDATE node_provisions SET status = ? WHERE id = ?`, st, id)
	return err
}

func (s *Store) SetNodeProvisionCloudResource(id int64, ocid, consoleURL string) error {
	_, err := s.Exec(`
		UPDATE node_provisions SET cloud_resource_id = ?, cloud_resource_url = ?
		WHERE id = ?`, nullable(ocid), nullable(consoleURL), id)
	return err
}

func (s *Store) SetNodeProvisionNode(id, nodeID int64) error {
	_, err := s.Exec(`UPDATE node_provisions SET node_id = ? WHERE id = ?`, nodeID, id)
	return err
}

func (s *Store) AppendNodeProvisionLog(id int64, line string) error {
	_, err := s.Exec(`UPDATE node_provisions SET log = log || ? WHERE id = ?`, line, id)
	return err
}

func (s *Store) SetNodeProvisionError(id int64, msg string) error {
	_, err := s.Exec(`
		UPDATE node_provisions
		SET status = 'failed', error = ?, ended_at = CURRENT_TIMESTAMP
		WHERE id = ?`, msg, id)
	return err
}

func (s *Store) DeleteNodeProvision(id int64) error {
	_, err := s.Exec(`DELETE FROM node_provisions WHERE id = ?`, id)
	return err
}

// AdvanceNodeProvisionByNode flips status bootstrap_pending → ready
// when the s3scanner first records a node row tied to a provision.
// Idempotent (returns advanced=false on subsequent calls).
func (s *Store) AdvanceNodeProvisionByNode(nodeID int64) (advanced bool, err error) {
	res, err := s.Exec(`
		UPDATE node_provisions
		SET status = 'ready', ended_at = CURRENT_TIMESTAMP
		WHERE node_id = ? AND status = 'bootstrap_pending'`, nodeID)
	if err != nil { return false, err }
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) AppendNodeProvisionLogByNode(nodeID int64, line string) error {
	_, err := s.Exec(`UPDATE node_provisions SET log = log || ? WHERE node_id = ?`, line, nodeID)
	return err
}

const nodeProvisionSelect = `
	SELECT id, display_name, region, cloud, credential_id, credential_name,
	       cloud_params_json, transport_config_id, status,
	       cloud_resource_id, cloud_resource_url, node_id,
	       log, error, est_cost_per_hour_usd, instance_shape,
	       created_at, started_at, ended_at,
	       created_by_user_id, created_by_email
	  FROM node_provisions`

type rowScanner interface{ Scan(...interface{}) error }

func scanNodeProvision(r rowScanner) (*NodeProvision, error) {
	var p NodeProvision
	if err := r.Scan(
		&p.ID, &p.DisplayName, &p.Region, &p.Cloud, &p.CredentialID, &p.CredentialName,
		&p.CloudParamsJSON, &p.TransportConfigID, &p.Status,
		&p.CloudResourceID, &p.CloudResourceURL, &p.NodeID,
		&p.Log, &p.Error, &p.EstCostPerHourUSD, &p.InstanceShape,
		&p.CreatedAt, &p.StartedAt, &p.EndedAt,
		&p.CreatedByUserID, &p.CreatedByEmail,
	); err != nil {
		return nil, fmt.Errorf("scan node_provision: %w", err)
	}
	return &p, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./controlplane/db/ -run TestNodeProvisionCRUD -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add controlplane/db/node_provisions.go controlplane/db/node_provisions_test.go
git commit -m "feat(db): node_provisions store CRUD + advance helpers"
```

---

## Task 3 — packaging: optional systemd unit

**Files:**
- Modify: `controlplane/packaging/packaging.go:457-494` (the `installShellScript` constant)
- Test: `controlplane/packaging/packaging_test.go` (extend if exists, else create)

The current install.sh nohups `okesu s3-jobs`. For managed deploys we want a systemd unit so the service survives reboots and the operator can `systemctl status okesu`. Detect whether systemd is present and, when it is, install a unit instead of nohup.

- [ ] **Step 1: Replace the post-enrollment lines in installShellScript**

Old (packaging.go ~485-494):
```sh
# Run enrollment — non-interactive; succeeds when the CP scanner
# responds via the bucket within ~10 minutes.
/usr/local/bin/okesu enroll --bootstrap /etc/okesu/bootstrap.json

# Start the S3-mode jobs runtime in the background. Production
# installs would register a service unit (systemd / launchd / rc.d /
# SMF); for the quick install we just nohup it. Operators can wire
# it through their service manager of choice afterwards.
nohup /usr/local/bin/okesu s3-jobs --bootstrap /etc/okesu/bootstrap.json >/var/log/okesu-jobs.log 2>&1 &
disown 2>/dev/null || true
```

New:
```sh
# Run enrollment — non-interactive; succeeds when the CP scanner
# responds via the bucket within ~10 minutes.
/usr/local/bin/okesu enroll --bootstrap /etc/okesu/bootstrap.json

# Prefer systemd when present (managed deploys + most production
# installs). nohup is the fallback for laptops / quick smoke tests
# where the operator just wants the daemon running this session.
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
    cat > /etc/systemd/system/okesu.service <<'UNIT'
[Unit]
Description=Okesu daemon (S3 jobs runtime)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/okesu s3-jobs --bootstrap /etc/okesu/bootstrap.json
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
UNIT
    systemctl daemon-reload
    systemctl enable --now okesu.service
    echo "okesu: enrolled, systemd unit started."
else
    nohup /usr/local/bin/okesu s3-jobs --bootstrap /etc/okesu/bootstrap.json >/var/log/okesu-jobs.log 2>&1 &
    disown 2>/dev/null || true
    echo "okesu: enrolled and running (nohup; no systemd detected)."
fi
```

- [ ] **Step 2: Test (just verify the rendered script contains both branches)**

```go
// controlplane/packaging/packaging_test.go (add a TestInstallShellScript_HasSystemdBranch)
func TestInstallShellScript_HasSystemdBranch(t *testing.T) {
	if !strings.Contains(installShellScript, "systemctl daemon-reload") {
		t.Error("install.sh missing systemd branch")
	}
	if !strings.Contains(installShellScript, "nohup") {
		t.Error("install.sh missing nohup fallback")
	}
}
```

Run: `go test ./controlplane/packaging/ -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add controlplane/packaging/packaging.go controlplane/packaging/packaging_test.go
git commit -m "feat(packaging): install.sh prefers systemd unit when available"
```

---

## Task 4 — API: POST /api/node-provision

**Files:**
- Create: `controlplane/api/node_provision.go`
- Create: `controlplane/api/node_provision_test.go`

Mirrors `cp_provision.go::CPProvisionCreateHandler`. Validates required fields, persists the row, kicks the worker goroutine, returns 202 + the row.

- [ ] **Step 1: Write the failing test**

```go
// controlplane/api/node_provision_test.go
package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestNodeProvisionCreate_HappyPath(t *testing.T) {
	st := newSeededTestStore(t)
	tcID, _ := st.CreateTransportConfig(...) // small helper
	body := map[string]any{
		"display_name": "edge-1",
		"region":       "us-phoenix-1",
		"cloud":        "oci",
		"credential_id": 1,
		"transport_config_id": tcID,
		"cloud_params": map[string]any{
			"shape": "VM.Standard.E4.Flex", "subnet_id": "x",
			"image_id": "y", "availability_domain": "z",
			"ocpus": 1, "memory_in_gbs": 8,
		},
	}
	buf, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/node-provision", bytes.NewReader(buf))
	NodeProvisionCreateHandler(st, fakeProvisionersOCI(), NodeProvisionWorkerConfig{Store: st})(rec, req)
	if rec.Code != 202 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["status"] != "queued" {
		t.Errorf("status=%v want queued", got["status"])
	}
}
```

- [ ] **Step 2: Run test (FAIL — handler doesn't exist)**

Run: `go test ./controlplane/api/ -run TestNodeProvisionCreate_HappyPath -v`
Expected: compile error.

- [ ] **Step 3: Write the handler**

```go
// controlplane/api/node_provision.go
package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/section9labs/okesu/controlplane/cpprovision"
	"github.com/section9labs/okesu/controlplane/db"
)

type nodeProvisionReq struct {
	DisplayName       string         `json:"display_name"`
	Region            string         `json:"region"`
	Cloud             string         `json:"cloud"`
	CredentialID      int64          `json:"credential_id"`
	CloudParams       map[string]any `json:"cloud_params"`
	TransportConfigID int64          `json:"transport_config_id"`
}

func NodeProvisionCreateHandler(
	store *db.Store,
	registry *cpprovision.Registry,
	workerCfg NodeProvisionWorkerConfig,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req nodeProvisionReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.DisplayName == "" || req.Region == "" || req.Cloud == "" ||
			req.CredentialID == 0 || req.TransportConfigID == 0 {
			http.Error(w, "missing required: display_name, region, cloud, credential_id, transport_config_id", http.StatusBadRequest)
			return
		}
		// transport_config must exist
		tc, err := store.GetTransportConfig(req.TransportConfigID)
		if err != nil {
			http.Error(w, "transport_config not found", http.StatusBadRequest)
			return
		}
		_ = tc
		// cloud_params required-key validation (delegated to per-cloud)
		if missing := nodeCloudParamsMissingFields(req.Cloud, req.CloudParams); len(missing) > 0 {
			http.Error(w, fmt.Sprintf("missing cloud_params: %v", missing), http.StatusBadRequest)
			return
		}
		paramsJSON, _ := json.Marshal(req.CloudParams)
		userID, userEmail := userFromContext(r)
		shape, _ := req.CloudParams["shape"].(string)
		row, err := store.InsertNodeProvision(db.NodeProvisionInsert{
			DisplayName:       req.DisplayName,
			Region:            req.Region,
			Cloud:             req.Cloud,
			CredentialID:      sqlInt64(req.CredentialID),
			CloudParamsJSON:   string(paramsJSON),
			TransportConfigID: req.TransportConfigID,
			InstanceShape:     shape,
			CreatedByUserID:   userID,
			CreatedByEmail:    userEmail,
		})
		if err != nil {
			http.Error(w, "insert: "+err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "node_provision.create",
			Target: fmt.Sprintf("node_provision:%d", row.ID),
		})
		if workerCfg.Store != nil {
			go RunNodeProvisionWorker(r.Context(), workerCfg, row.ID)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(toNodeProvisionJSON(row))
	}
}

// nodeCloudParamsMissingFields lists per-cloud required cloud_params
// keys. Mirrors the OCI provisioner's validation so the form blocks
// before the worker rejects.
func nodeCloudParamsMissingFields(cloud string, params map[string]any) []string {
	required := map[string][]string{
		"oci": {"shape", "subnet_id", "image_id", "availability_domain", "ocpus", "memory_in_gbs"},
		"aws": {"instance_type", "subnet_id", "ami_id"},
	}
	keys, ok := required[cloud]
	if !ok { return nil }
	var missing []string
	for _, k := range keys {
		if v, has := params[k]; !has || v == "" || v == nil {
			missing = append(missing, k)
		}
	}
	return missing
}

func toNodeProvisionJSON(p *db.NodeProvision) map[string]any {
	return map[string]any{
		"id":                  p.ID,
		"display_name":        p.DisplayName,
		"region":              p.Region,
		"cloud":               p.Cloud,
		"transport_config_id": p.TransportConfigID,
		"status":              p.Status,
		"cloud_resource_id":   p.CloudResourceID.String,
		"cloud_resource_url":  p.CloudResourceURL.String,
		"node_id":             p.NodeID.Int64,
		"created_at":          p.CreatedAt,
	}
}
```

- [ ] **Step 4: Run test to PASS**

Run: `go test ./controlplane/api/ -run TestNodeProvisionCreate_HappyPath -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add controlplane/api/node_provision.go controlplane/api/node_provision_test.go
git commit -m "feat(api): POST /api/node-provision creates managed-deploy row"
```

---

## Task 5 — Worker: launch VM + upload package + render cloud-init

**Files:**
- Create: `controlplane/api/node_provision_worker.go`
- Test: `controlplane/api/node_provision_worker_test.go`

The worker runs in a goroutine after row creation. State machine:
`queued → cloud_init_running` (after we hand the LaunchInstance request) → `bootstrap_pending` (after VM is RUNNING) → `ready` (after s3scanner advances via `AdvanceNodeProvisionByNode`).

- [ ] **Step 1: Implement the worker (no failing-test-first because it's a goroutine-driven state machine; write a hermetic test in step 2)**

```go
// controlplane/api/node_provision_worker.go
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/section9labs/okesu/controlplane/adapters/s3blob"
	"github.com/section9labs/okesu/controlplane/cpprovision"
	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/packaging"
)

type NodeProvisionWorkerConfig struct {
	Store         *db.Store
	Registry      *cpprovision.Registry
	BinaryPaths   map[string][]byte // os-arch → bytes; populated by server.go
}

func RunNodeProvisionWorker(ctx context.Context, cfg NodeProvisionWorkerConfig, id int64) {
	store := cfg.Store
	logf := func(format string, args ...any) {
		_ = store.AppendNodeProvisionLog(id, fmt.Sprintf(format, args...)+"\n")
	}
	failNow := func(msg string) {
		_ = store.SetNodeProvisionError(id, msg)
		logf("✗ %s", msg)
	}

	row, err := store.NodeProvision(id)
	if err != nil { return }

	tc, err := store.GetTransportConfig(row.TransportConfigID)
	if err != nil { failNow("transport_config: "+err.Error()); return }
	if !tc.AccessKey.Valid || !tc.SecretKey.Valid {
		failNow("transport_config has no access keys"); return
	}

	cred, err := store.GetCloudCredential(row.CredentialID.Int64)
	if err != nil { failNow("cloud credential: "+err.Error()); return }
	mk, err := store.MasterKeyFromMeta()
	if err != nil { failNow("master key: "+err.Error()); return }
	credBytes, err := store.DecryptCloudCredential(cred.ID, mk)
	if err != nil { failNow("decrypt credential: "+err.Error()); return }

	// 1. Build the enrollment package against the transport_config.
	// Reuse the same packaging.Build pipeline the manual S3 tab uses.
	pkg, err := store.CreateEnrollmentPackage(db.EnrollmentPackage{
		TransportConfigID: row.TransportConfigID,
		DisplayName:       row.DisplayName + "-managed",
	})
	if err != nil { failNow("enrollment_package row: "+err.Error()); return }
	logf("✓ minted enrollment_package %d", pkg.ID)

	build, err := packaging.Build(packaging.BuildRequest{
		DisplayName: row.DisplayName,
		PackageID:   pkg.ID,
		Bucket:      tc.Bucket, Endpoint: tc.Endpoint, Region: tc.Region.String,
		UseSSL:      tc.UseSSL,
		AccessKey:   tc.AccessKey.String, SecretKey: tc.SecretKey.String,
		CPID:        tc.CPID.String,
		FleetPubkeyPEM: tc.FleetPubkeyPEM.String,
		PackageCertPEM: pkg.PackageCertPEM, PackageKeyPEM: pkg.PackageKeyPEM,
		Binaries:    cfg.BinaryPaths,
		Format:      "tar.gz",
	})
	if err != nil { failNow("packaging: "+err.Error()); return }
	logf("✓ built package (%d bytes)", len(build.Bytes))

	// 2. Upload to bucket so the VM can fetch via presigned URL.
	blob, err := s3blob.New(ctx, s3blob.Config{
		Endpoint: tc.Endpoint, Region: tc.Region.String, Bucket: tc.Bucket,
		AccessKey: tc.AccessKey.String, SecretKey: tc.SecretKey.String,
		UseSSL: tc.UseSSL,
	})
	if err != nil { failNow("s3 client: "+err.Error()); return }
	bundleKey := fmt.Sprintf("provisions/nodes/%d/package.tar.gz", id)
	if err := blob.Put(ctx, bundleKey, bytes.NewReader(build.Bytes), "application/gzip"); err != nil {
		failNow("upload package: "+err.Error()); return
	}
	presignedURL, err := blob.PresignedGetURL(ctx, bundleKey, time.Hour)
	if err != nil { failNow("presign url: "+err.Error()); return }
	logf("✓ package uploaded; presigned URL valid 1h")

	// 3. Render cloud-init.
	cloudInit := nodeCloudInit(presignedURL, build.Filename)

	// 4. Decode cloud_params + Launch.
	var params map[string]any
	_ = json.Unmarshal([]byte(row.CloudParamsJSON), &params)
	prov, err := cfg.Registry.Get(row.Cloud)
	if err != nil { failNow("provisioner not registered: "+row.Cloud); return }
	_ = store.UpdateNodeProvisionStatus(id, db.NodeProvisionCloudInitRunning)
	logf("→ calling Provisioner.Launch")

	launchRes, err := prov.Launch(ctx, cpprovision.LaunchRequest{
		DisplayName: row.DisplayName, Region: row.Region,
		CloudParams: params, CredsRaw: credBytes,
		CloudInitScript: cloudInit,
	})
	if err != nil { failNow("Launch: "+err.Error()); return }
	logf("✓ instance running at %s", launchRes.PublicIP)
	_ = store.SetNodeProvisionCloudResource(id, launchRes.ResourceID, launchRes.ConsoleURL)
	_ = store.UpdateNodeProvisionStatus(id, db.NodeProvisionBootstrapPending)
	logf("→ awaiting node enrollment via S3")
}

func nodeCloudInit(bundleURL, filename string) string {
	// Reuse the same install pattern as the CP cloud-init: install
	// curl + tar + jq, fetch presigned URL, untar, run install.sh.
	// install.sh writes a systemd unit when systemd is present
	// (Task 3) so we don't need any extra service-management glue.
	return fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
exec > >(tee -a /var/log/okesu-bootstrap.log) 2>&1
echo "==> okesu node managed bootstrap"

if command -v dnf >/dev/null 2>&1; then
    dnf install -y curl tar
elif command -v apt-get >/dev/null 2>&1; then
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -y
    apt-get install -y curl tar ca-certificates
fi

mkdir -p /opt/okesu-node
cd /opt/okesu-node
curl -fSL --retry 6 --retry-delay 5 -o "%s" "%s"
tar -xzf "%s"
cd "$(find . -mindepth 1 -maxdepth 1 -type d | head -1)"
sudo ./install.sh
echo "==> done"
`, filename, strings.ReplaceAll(bundleURL, `"`, `\"`), filename)
}
```

- [ ] **Step 2: Add a hermetic test (with a mock provisioner)**

```go
// node_provision_worker_test.go
type mockNodeProv struct{ launched bool }
func (m *mockNodeProv) Cloud() string { return "oci" }
func (m *mockNodeProv) Launch(_ context.Context, _ cpprovision.LaunchRequest) (*cpprovision.LaunchResult, error) {
	m.launched = true
	return &cpprovision.LaunchResult{ResourceID: "ocid1.test", PublicIP: "1.2.3.4", ConsoleURL: "https://x"}, nil
}
func (m *mockNodeProv) Destroy(_ context.Context, _ string, _ []byte) error { return nil }

func TestNodeProvisionWorker_HappyPath(t *testing.T) {
	// ... seed store with cred + transport_config + provision row ...
	// ... start a fake S3 with httptest (or use a minio in-process if available) ...
	// run worker, expect status=bootstrap_pending and provisioner.launched=true.
}
```

(Implementer: a full mock S3 is too heavy for one task — use a stub that bypasses the s3blob put + presign. Acceptable to extract the s3 upload into a small interface for testability.)

- [ ] **Step 3: Run + commit**

Run: `go test ./controlplane/api/ -run TestNodeProvisionWorker -v`
Expected: PASS.

```bash
git add controlplane/api/node_provision_worker.go controlplane/api/node_provision_worker_test.go
git commit -m "feat(api): node provision worker — package upload + cloud-init + Launch"
```

---

## Task 6 — API: list / get / delete + estimate

**Files:**
- Create: `controlplane/api/node_provision_list.go`
- Create: `controlplane/api/node_provision_estimate.go`
- Modify: `controlplane/server.go` (route registration)

- [ ] **Step 1: List + Get + Delete handlers**

```go
// node_provision_list.go
func NodeProvisionsListHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := store.ListNodeProvisions(parseLimit(r, 50))
		if err != nil { http.Error(w, err.Error(), 500); return }
		out := make([]map[string]any, len(rows))
		for i := range rows { out[i] = toNodeProvisionJSON(&rows[i]) }
		writeJSON(w, 200, out)
	}
}

func NodeProvisionGetHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		row, err := store.NodeProvision(id)
		if err != nil { http.Error(w, "not found", 404); return }
		writeJSON(w, 200, toNodeProvisionJSON(row))
	}
}

func NodeProvisionDeleteHandler(
	store *db.Store, registry *cpprovision.Registry,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		destroy := r.URL.Query().Get("destroy") == "true"
		row, err := store.NodeProvision(id)
		if err != nil { http.Error(w, "not found", 404); return }

		// Optional cloud-side teardown.
		if destroy && row.CloudResourceID.Valid {
			cred, _ := store.GetCloudCredential(row.CredentialID.Int64)
			mk, _ := store.MasterKeyFromMeta()
			credBytes, _ := store.DecryptCloudCredential(cred.ID, mk)
			prov, perr := registry.Get(row.Cloud)
			if perr == nil {
				_ = prov.Destroy(r.Context(), row.CloudResourceID.String, credBytes)
			}
		}
		// Cleanup gap from #129 follow-up: also remove the matching
		// nodes row (if registered) so the operator doesn't see a
		// ghost in the node list.
		if row.NodeID.Valid {
			_ = store.DeleteNode(row.NodeID.Int64)
		}
		_ = store.DeleteNodeProvision(id)
		w.WriteHeader(http.StatusNoContent)
	}
}
```

- [ ] **Step 2: Estimate handler**

```go
// node_provision_estimate.go
func NodeProvisionEstimateHandler(store *db.Store, catalog *cpprovision.CostCatalog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Cloud string `json:"cloud"`; CredentialID int64 `json:"credential_id"`
			CloudParams map[string]any `json:"cloud_params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		shape, _ := req.CloudParams["shape"].(string)
		ocpus, _ := toFloat(req.CloudParams["ocpus"])
		memGB, _ := toFloat(req.CloudParams["memory_in_gbs"])
		usd := catalog.HourlyUSD(req.Cloud, shape, ocpus, memGB)
		writeJSON(w, 200, map[string]any{"hourly_usd": usd})
	}
}
```

- [ ] **Step 3: Wire in `server.go`**

Locate the section where `cp-provision` routes register (around `r.Post("/api/federation/cp-provision", ...)`) and add:

```go
r.Get("/api/node-provisions", api.NodeProvisionsListHandler(s.store))
r.Get("/api/node-provisions/{id}", api.NodeProvisionGetHandler(s.store))
r.Post("/api/node-provision", api.NodeProvisionCreateHandler(s.store, s.cpProvisioners, api.NodeProvisionWorkerConfig{
    Store: s.store, Registry: s.cpProvisioners,
    BinaryPaths: s.daemonBinaryBlobs(), // helper that returns map[os-arch][]byte
}))
r.Post("/api/node-provision/estimate", api.NodeProvisionEstimateHandler(s.store, s.costCatalog))
r.Delete("/api/node-provisions/{id}", api.NodeProvisionDeleteHandler(s.store, s.cpProvisioners))
```

- [ ] **Step 4: Smoke test (manual)**

```bash
make redeploy-local
curl -sk -c /tmp/cookies.txt -X POST https://localhost:7443/api/auth/login \
  -H 'Content-Type: application/json' -d '{"email":"admin@local","password":"okesu-demo"}' -o /dev/null
curl -sk -b /tmp/cookies.txt https://localhost:7443/api/node-provisions
```
Expected: `[]` (empty list).

- [ ] **Step 5: Commit**

```bash
git add controlplane/api/node_provision_list.go controlplane/api/node_provision_estimate.go controlplane/server.go
git commit -m "feat(api): node-provisions list/get/delete/estimate + route wiring"
```

---

## Task 7 — s3scanner advances node_provisions on first registration

**Files:**
- Modify: `controlplane/transport/s3scanner/scanner.go`

- [ ] **Step 1: Locate where the scanner inserts a node row after a successful registration. Look for `s.store.UpsertNode` or `RegisterNode` near `recordRegistration`/`scanRegistrations`.**

```bash
grep -n "InsertNode\|UpsertNode\|RegisterNode\|store\..*Node" controlplane/transport/s3scanner/scanner.go | head -10
```

- [ ] **Step 2: Right after a successful node insert, call `AdvanceNodeProvisionByNode(nodeID)`. Pattern from PR #129's s3reader change:**

```go
nodeID := /* ... existing code that produced the just-registered node ID ... */
if advanced, err := s.store.AdvanceNodeProvisionByNode(nodeID); err != nil {
    log.Printf("s3scanner advance node_provision: %v", err)
} else if advanced {
    _ = s.store.AppendNodeProvisionLogByNode(nodeID,
        "✓ first registration received over S3 — node provisioning ready\n")
}
```

- [ ] **Step 3: Test (extend scanner_test if it exists; otherwise integration-test via `make redeploy-local` once Task 8 lands).**

- [ ] **Step 4: Commit**

```bash
git add controlplane/transport/s3scanner/scanner.go
git commit -m "feat(s3scanner): flip node_provisions to ready when node first registers"
```

---

## Task 8 — UI: Managed deploy tab + provisions panel

**Files:**
- Create: `web/src/components/nodes/AddNodeManaged.tsx`
- Create: `web/src/components/nodes/NodeProvisionsPanel.tsx`
- Modify: `web/src/api.ts` (new methods + types)
- Modify: `web/src/pages/Nodes.tsx` (add tab + panel)

The shape is a **direct port** of `web/src/pages/Federation.tsx::ManagedDeployPanel` and `ManagedDeploysPanel` — same fields, same UX, just hitting the node endpoints.

- [ ] **Step 1: Add API client methods**

```ts
// web/src/api.ts (add near cpProvisionsList block)
export interface NodeProvision {
  id: number; display_name: string; region: string; cloud: string;
  transport_config_id: number; status: string;
  cloud_resource_id?: string; cloud_resource_url?: string;
  node_id?: number; created_at: string;
}

  nodeProvisionsList: () => request<NodeProvision[]>('/api/node-provisions'),
  nodeProvision: (id: number) => request<NodeProvision>(`/api/node-provisions/${id}`),
  nodeProvisionCreate: (req: NodeProvisionRequest) =>
    request<NodeProvision>('/api/node-provision', { method: 'POST', body: JSON.stringify(req) }),
  nodeProvisionDelete: (id: number, destroy = false) =>
    request<void>(`/api/node-provisions/${id}${destroy ? '?destroy=true' : ''}`, { method: 'DELETE' }),
  nodeProvisionEstimate: (req: { cloud: string; credential_id: number; cloud_params: Record<string, unknown> }) =>
    request<{ hourly_usd: number }>('/api/node-provision/estimate', { method: 'POST', body: JSON.stringify(req) }),
```

- [ ] **Step 2: AddNodeManaged.tsx — port ManagedDeployPanel**

Open `web/src/pages/Federation.tsx` around line 632 (`function ManagedDeployPanel`). Copy the entire function into `web/src/components/nodes/AddNodeManaged.tsx`, rename it `AddNodeManaged`, change the api calls (`cpProvisionCreate` → `nodeProvisionCreate`, `cpProvisionEstimate` → `nodeProvisionEstimate`, `cpProvisionersList` → reuse, `cloudCredentialsList` → reuse), drop the transport-mode radios (managed deploys for nodes always go via S3), keep only the `transport_config_id` picker.

Submit body:
```ts
const row = await api.nodeProvisionCreate({
  display_name: displayName, region, cloud,
  credential_id: credentialID,
  cloud_params: cloudParams,
  transport_config_id: transportConfigID,
});
```

- [ ] **Step 3: NodeProvisionsPanel.tsx — port ManagedDeploysPanel**

Same approach: `web/src/pages/Federation.tsx::ManagedDeploysPanel` (line 1400). Copy + rename + swap api calls.

- [ ] **Step 4: Wire into Nodes.tsx**

In `AddNodeModal`:
```tsx
type AddTab = 'ssh' | 's3' | 'managed';
// ... extend the tab nav with a "Managed deploy" button ...
{tab === 'managed' && <AddNodeManaged onClose={onClose} />}
```

Below the existing node list in `<Nodes/>`, add `<NodeProvisionsPanel />`.

- [ ] **Step 5: Smoke test in browser**

```bash
make redeploy-local
```
Open `https://localhost:7443/nodes`. Click Add Node → Managed deploy. Verify form renders. Submit (with the OCI credentials from the prior PR). Watch the row appear in `<NodeProvisionsPanel />`. Hard-refresh tabs (`⌘⇧R`).

- [ ] **Step 6: Commit**

```bash
git add web/src/api.ts web/src/pages/Nodes.tsx web/src/components/nodes/
git commit -m "feat(web/nodes): managed-deploy tab + in-flight provisions panel"
```

---

## Task 9 — Cleanup: orphan peer / node + bundle GC

**Files:**
- Modify: `controlplane/api/cp_provision.go` (`CPProvisionDeleteHandler`)
- Modify: `controlplane/federation/s3reader/reader.go` (delete bootstrap blob after first success)
- Modify: `controlplane/api/cp_provision_worker.go` (or a new helper file) — bundle-GC entry point used by both the destroy path and the s3reader hook.

Per the cleanup follow-ups from PR #129.

- [ ] **Step 1: Cp_provision destroy also tears down peer + bundle**

In `CPProvisionDeleteHandler`, after the existing `Provisioner.Destroy` call, before `DeleteCPProvision`:

```go
if row.PeerID.Valid {
    _ = store.DeleteFederationPeer(row.PeerID.Int64)
}
// Best-effort: remove the one-time bundle blob from the bucket.
if row.TransportConfigID.Valid {
    if tc, err := store.GetTransportConfig(row.TransportConfigID.Int64); err == nil {
        _ = deleteBootstrapBundle(r.Context(), tc, row.ID)
    }
}
```

`deleteBootstrapBundle(ctx, tc, provisionID)` is a new helper in `cp_provision_worker.go`:

```go
func deleteBootstrapBundle(ctx context.Context, tc db.TransportConfig, childInstanceID string) error {
    blob, err := s3blob.New(ctx, s3blob.Config{...})
    if err != nil { return err }
    return blob.Delete(ctx, fmt.Sprintf("cp/%s/bootstrap/bundle.tar.gz", childInstanceID))
}
```

(We need childInstanceID, which we don't currently store on cp_provisions. Add a `child_instance_id TEXT` column in this task's migration, or look up via federation_peers.bucket_prefix parsing — pick the cleaner one. The implementer should add the column.)

- [ ] **Step 2: s3reader: delete the bootstrap bundle on first successful introspect**

Right after the `AppendCPProvisionLogByPeer(...)` call we added in PR #129:

```go
if advanced {
    // Cleanup: the child has it from here on. Best-effort delete.
    go deleteBootstrapBundleByPeer(ctx, l.store, p.ID)
}
```

- [ ] **Step 3: Tests**

Add a unit test for the destroy path that verifies the federation_peers row + bootstrap blob are gone. Use a fake `BlobStore` that records Delete calls.

- [ ] **Step 4: Commit**

```bash
git add controlplane/api/cp_provision.go controlplane/api/cp_provision_worker.go controlplane/federation/s3reader/reader.go
git commit -m "fix(federation/s3): destroy + first-success cleanup of peer rows + bootstrap blobs"
```

---

## Task 10 — End-to-end on OCI

- [ ] **Step 1: Pre-req — make sure transport_config exists**

The global CP already has `transport_configs.id=2` (Okesu Fleet) from PR #129's debugging. Reuse it.

- [ ] **Step 2: Submit a managed node provision**

```bash
curl -sk -c /tmp/cookies.txt -X POST https://localhost:7443/api/auth/login \
  -H 'Content-Type: application/json' -d '{"email":"admin@local","password":"okesu-demo"}' -o /dev/null
cat > /tmp/node-provision.json <<EOF
{
  "display_name": "oci-edge-1",
  "region": "us-phoenix-1",
  "cloud": "oci",
  "credential_id": 5,
  "transport_config_id": 2,
  "cloud_params": {
    "availability_domain": "BzLN:PHX-AD-1",
    "compartment_id": "ocid1.compartment.oc1..aaaaaaaafpw2xr2fftdleu6sxej7l4pz75h7drmuvel354e4creoij246wva",
    "image_id": "ocid1.image.oc1.phx.aaaaaaaarr2iksamp2hjolodtyhmbxho6k7pweau3ke6szdyh3lsztau2cea",
    "memory_in_gbs": 4,
    "ocpus": 1,
    "shape": "VM.Standard.E4.Flex",
    "ssh_authorized_keys": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFPaFR88uItsfwSs08KG3PzU1SLXoQvI0n/Q/SSTmZfR matt@s9labs",
    "subnet_id": "ocid1.subnet.oc1.phx.aaaaaaaan33wnntzruwvenxcsm7qbtlkwgs5gqdlgotxvk6xprotigri47lq"
  }
}
EOF
curl -sk -b /tmp/cookies.txt -X POST https://localhost:7443/api/node-provision \
  -H 'Content-Type: application/json' -d @/tmp/node-provision.json
```
Expected: HTTP 202 + JSON row with `status=queued`.

- [ ] **Step 3: Wait ~5–7 min, verify**

```bash
sqlite3 test/stack/run-fed/global/cp.db "SELECT id, status, node_id, substr(log, -300) FROM node_provisions ORDER BY id DESC LIMIT 1;"
sqlite3 test/stack/run-fed/global/cp.db "SELECT id, name, transport, last_seen_at FROM nodes ORDER BY id DESC LIMIT 1;"
```
Expected: `status=ready`, `node_id` set, fresh `last_seen_at` on the node.

- [ ] **Step 4: Run an agent on the node**

In the UI, on the new node's detail page, click "Run agent" → pick an agent like `edr` → submit. Watch the run reach `succeeded`.

- [ ] **Step 5: Run an orchestration**

Find a simple orchestration in `examples/orchestrations/` that targets `agents=edr`. From the global CP UI, dispatch it scoped to the new node. Verify it reaches `succeeded`.

- [ ] **Step 6: Final commit + rebase if needed; open PR**

```bash
gh pr create --title "feat(nodes): managed deploy — provision OCI VMs as nodes via S3" --body @docs/superpowers/plans/2026-05-04-node-managed-deploy.md
```

---

## Out of scope / explicit non-goals

- **Cross-CP transport_config sharing** — the parent UI listing transport_configs from federated child CPs is v2. v1 requires the operator to know which `transport_config_id` corresponds to which CP's bucket.
- **AWS managed-node deploy** — schema + interface support it; AWS provisioner already exists; UI just needs a small AWS-specific cloud_params form. Treat as a small follow-up.
- **Bundle re-use** — each provision builds + uploads a fresh package. Re-using packages across provisions is an optimization; not worth it at this scale.
- **Federation write-pipe-driven node deploy** — the global CP forwarding a node provision to a federated child CP (which then uses ITS OWN cloud creds to launch) is v2 and depends on the child having cloud_credentials of its own. Out of scope.
