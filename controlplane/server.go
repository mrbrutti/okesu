package controlplane

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/section9labs/okesu/agent"
	"github.com/section9labs/okesu/agent/s3transport"
	"github.com/section9labs/okesu/controlplane/adapters/clickhouseevents"
	"github.com/section9labs/okesu/controlplane/adapters/inprocess"
	"github.com/section9labs/okesu/controlplane/adapters/kafka"
	"github.com/section9labs/okesu/controlplane/adapters/redispubsub"
	"github.com/section9labs/okesu/controlplane/adapters/sqliteevents"
	"github.com/section9labs/okesu/controlplane/api"
	"github.com/section9labs/okesu/controlplane/api/enrichment"
	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/cpprovision"
	awsprovisioner "github.com/section9labs/okesu/controlplane/cpprovision/aws"
	ociprovisioner "github.com/section9labs/okesu/controlplane/cpprovision/oci"
	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/eventpipeline"
	"github.com/section9labs/okesu/controlplane/federation"
	"github.com/section9labs/okesu/controlplane/federation/s3publisher"
	"github.com/section9labs/okesu/controlplane/federation/s3reader"
	"github.com/section9labs/okesu/controlplane/federation/s3rpc"
	"github.com/section9labs/okesu/controlplane/ioc/catalog"
	"github.com/section9labs/okesu/controlplane/jobs"
	"github.com/section9labs/okesu/controlplane/notify"
	"github.com/section9labs/okesu/controlplane/orchestrator"
	"github.com/section9labs/okesu/controlplane/packaging"
	"github.com/section9labs/okesu/controlplane/ports"
	"github.com/section9labs/okesu/controlplane/sshdeploy"
	"github.com/section9labs/okesu/controlplane/transport/s3scanner"
	"github.com/section9labs/okesu/controlplane/tunnel"
	"github.com/section9labs/okesu/controlplane/ui"
)

// Server holds all the long-lived state for the Control Plane HTTP server.
type Server struct {
	cfg        Config
	store      *db.Store
	eventStore ports.EventStore
	queue      ports.Queue
	secrets    ports.Secrets
	mgr        *auth.Manager
	bcast      *Broadcaster
	ca         *CA
	oidc       *auth.OIDCProvider // nil when OIDC not configured
	jobs       *jobs.Registry
	tunReg     *tunnel.Registry
	runs       *api.RunRegistry
	orchestra  *api.OrchestrationCoordinator // Phase A orchestrator
	notify     *notify.Worker
	fedPoller  *federation.Poller     // Phase 9 parent-side federation
	fedAgg     *federation.Aggregator // Phase 9.6 federated reads
	// fedFindingFanout polls child CPs for newly-projected findings
	// and routes them through the local orchestration coordinator's
	// OnFinding hook. See controlplane/api/federation_finding_fanout.go.
	fedFindingFanout *api.FederationFindingFanout
	// cpProvisioners is the registry of per-cloud CP-provisioning
	// implementations. Per-cloud impls (OCI in 21.3b, AWS in 21.3c)
	// register against this from server.New() below.
	cpProvisioners *cpprovision.Registry
	// bundleCache holds the generated child-CP bundle bytes the
	// cloud-init script fetches via /api/federation/cp-bundle/download.
	// Populated by RunCPProvisionWorker, drained by the bootstrap
	// handler when the new CP completes its bootstrap exchange.
	bundleCache *api.BundleCache
	http        *http.Server
	mgmtHTTP    *http.Server // mTLS-protected management plane
}

// daemonBinaryVersion reports the version of the daemon binary the CP
// would push on a deploy or update-binary action. By convention the
// daemon is built from the same repo checkout as the CP, so we return
// our own controlplane.Version() as the canonical value. Operators
// who keep a custom daemon binary at --daemon-binary should override
// this assumption (TODO: shell out to the binary's version subcommand).
func (s *Server) daemonBinaryVersion() string {
	return Version()
}

// IssueClientCert satisfies api.NodeDeployer. Used by the deploy flow to
// generate per-agent mTLS client cert bundles on demand.
func (s *Server) IssueClientCert(agent string) (cert, key, ca []byte, err error) {
	c, k, err := s.ca.IssueClientCert(agent)
	if err != nil {
		return nil, nil, nil, err
	}
	return c, k, s.ca.CertPEM, nil
}

// New constructs a Server. It opens (and migrates) the database,
// resolves any unset secrets through the configured ports.Secrets
// adapter (Phase 8g), ensures the admin user exists, generates a
// self-signed TLS cert if needed, and wires the HTTP routes.
func New(cfg Config) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// Phase 8g: resolve secrets BEFORE we use any of them downstream.
	// Three layers, applied in order:
	//   1. resolveConfigSecretRefs — substitute "${secret:NAME}"
	//      references that the YAML config file embedded.
	//   2. resolveSecrets — for canonical-named secrets the CP knows
	//      it needs, pull from the adapter when the field is still
	//      empty (operator didn't pass via flag/env or YAML).
	// Operators using legacy --admin-password etc. CLI flags get a
	// deprecation warning at this point.
	secrets, secretsLabel, serr := buildSecrets(cfg.SecretsSource)
	if serr != nil {
		return nil, fmt.Errorf("secrets source: %w", serr)
	}
	log.Printf("secrets source: %s", secretsLabel)
	{
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := resolveConfigSecretRefs(ctx, &cfg, secrets); err != nil {
			cancel()
			return nil, fmt.Errorf("resolve config secret refs: %w", err)
		}
		if err := resolveSecrets(ctx, &cfg, secrets); err != nil {
			cancel()
			return nil, fmt.Errorf("resolve secrets: %w", err)
		}
		cancel()
	}

	store, err := db.Open(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	if _, err := auth.SeedAdmin(store, cfg.AdminEmail, cfg.AdminPassword); err != nil {
		return nil, fmt.Errorf("seed admin: %w", err)
	}

	// Phase 21.1 — first-boot bootstrap from a parent-issued bundle.
	// When OKESU_CP_BOOTSTRAP_TOKEN is set AND cp_meta has no
	// federation token yet, exchange the bootstrap token for a
	// long-lived rotating peer token. Failures fall through to the
	// normal flag/env path; the bootstrap token is one-shot at the
	// parent regardless.
	if bootToken, berr := MaybeBootstrapFromEnv(store, cfg.EffectiveMgmtURL()); berr != nil {
		log.Printf("bootstrap: %v (continuing without federation)", berr)
	} else if bootToken != "" {
		// Override cfg.FederationToken so the cp_meta update below
		// stores the parent-minted token rather than whatever was
		// passed via --federation-token.
		cfg.FederationToken = bootToken
	}

	// Phase 9: bootstrap / refresh cp_meta. The first call ensures a
	// stable instance_id is generated; subsequent calls overwrite the
	// operator-tunable fields (region, display name, federation token)
	// from flags/env so a config change picks up cleanly on restart
	// without a SQL migration. Token is only rotated when explicitly
	// set; "-" (sentinel) clears it. Empty leaves the existing hash
	// untouched, so an operator can launch once with the token and
	// then re-launch with the flag empty without losing federation.
	tokenOp := cfg.FederationToken
	if tokenOp == "" {
		// preserve existing hash
	}
	// Phase A.1 — when --cp-instance-id is set AND cp_meta has no
	// row yet, seed bootstrap with that exact uuid. The federation
	// enrollment bundle uses this so the parent can pre-register the
	// child's bucket prefix before the child has even booted. On a
	// re-launched CP this is a no-op: the row exists, we keep its
	// already-issued id (CPMeta() returns it unchanged).
	if cfg.CPInstanceID != "" {
		if err := store.SeedCPInstanceID(cfg.CPInstanceID); err != nil {
			return nil, fmt.Errorf("cp_meta seed instance_id: %w", err)
		}
	}
	if _, err := store.UpdateCPMeta(cfg.CPRegion, cfg.CPDisplayName, "", tokenOp); err != nil {
		return nil, fmt.Errorf("cp_meta init: %w", err)
	}

	mgr, err := auth.NewManager(store, cfg.SessionKey)
	if err != nil {
		return nil, fmt.Errorf("session manager: %w", err)
	}

	// Phase 8d: pick the PubSub adapter for SSE fan-out + run-subscriber
	// notifications. Empty URL keeps the in-process adapter (single-CP
	// dev default); a Redis URL switches to the redis adapter so fan-out
	// works across CP replicas.
	var pubsub ports.PubSub
	if cfg.PubSubURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		rps, perr := redispubsub.New(ctx, cfg.PubSubURL)
		cancel()
		if perr != nil {
			return nil, fmt.Errorf("redis pubsub: %w", perr)
		}
		pubsub = rps
		log.Printf("pubsub: redis (%s)", cfg.PubSubURL)
	} else {
		pubsub = inprocess.NewPubSub()
		log.Printf("pubsub: in-process (single-CP)")
	}
	bcast := NewBroadcasterWith(pubsub)

	ca, err := EnsureCA(cfg)
	if err != nil {
		return nil, fmt.Errorf("ensure ca: %w", err)
	}

	// Phase 8c.next: events flow through the EventStore port. Adapter
	// selection is config: --events-store=clickhouse swaps the SQLite
	// wrapper for a real ClickHouse cluster. The webhook handler is
	// adapter-blind — same Insert call against either.
	var eventStore ports.EventStore
	switch strings.ToLower(cfg.EventsStore) {
	case "clickhouse":
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		ch, cherr := clickhouseevents.New(ctx, clickhouseevents.Config{
			Addr:     cfg.ClickHouseAddrs,
			Database: cfg.ClickHouseDatabase,
			Username: cfg.ClickHouseUsername,
			Password: cfg.ClickHousePassword,
			Secure:   cfg.ClickHouseSecure,
		})
		cancel()
		if cherr != nil {
			return nil, fmt.Errorf("clickhouse events: %w", cherr)
		}
		eventStore = ch
		log.Printf("events store: clickhouse (%s)", cfg.ClickHouseAddrs)
	default:
		eventStore = sqliteevents.New(store)
		log.Printf("events store: sqlite (single-CP default)")
	}

	// Queue selection. The eventpipeline worker bridges queue → events
	// store; both halves are pluggable. Default is inprocess for dev.
	var queue ports.Queue
	switch strings.ToLower(cfg.Queue) {
	case "kafka":
		kq, kerr := kafka.New(kafka.Config{
			Brokers:      cfg.KafkaBrokers,
			SASLUsername: cfg.KafkaSASLUsername,
			SASLPassword: cfg.KafkaSASLPassword,
			UseTLS:       cfg.KafkaUseTLS,
		})
		if kerr != nil {
			return nil, fmt.Errorf("kafka queue: %w", kerr)
		}
		queue = kq
		log.Printf("queue: kafka (%s)", cfg.KafkaBrokers)
	default:
		queue = inprocess.NewQueue()
		log.Printf("queue: in-process (single-CP default)")
	}

	// Start the worker that drains the queue into the events store.
	// One per CP replica; consumer-group coordination handles fanout
	// when multiple replicas run.
	pipelineCtx, pipelineCancel := context.WithCancel(context.Background())
	pipelineWorker := eventpipeline.NewWorker(queue, eventStore, store, eventpipeline.Config{})
	// Worker.Run is started below, after the orchestrator coordinator
	// is constructed — that lets us install the finding-trigger hook
	// before any events flow through.
	_ = pipelineCancel // wired into Server.Stop in a follow-up

	// Phase 9: install the cert-fingerprint helper that lets the
	// db package look up enrollment_packages by their embedded
	// signing certificate. The packaging package owns the actual
	// hash logic; we route through this hook to avoid an import
	// cycle (db → packaging would pull a lot more in).
	db.SetCertFingerprintFn(packaging.CertFingerprint)

	srv := &Server{
		cfg:            cfg,
		store:          store,
		eventStore:     eventStore,
		queue:          queue,
		secrets:        secrets,
		mgr:            mgr,
		bcast:          bcast,
		ca:             ca,
		jobs:           jobs.New(500),
		tunReg:         tunnel.NewRegistry(),
		runs:           api.NewRunRegistry(),
		cpProvisioners: cpprovision.NewRegistry(),
		bundleCache:    api.NewBundleCache(),
	}
	// Phase 21.3b/c — register per-cloud provisioners. Each cloud
	// implementation lives in its own subpackage so adding a new one
	// is one import + one Register() call.
	srv.cpProvisioners.Register(ociprovisioner.New())
	srv.cpProvisioners.Register(awsprovisioner.New())
	srv.notify = &notify.Worker{
		Store:      store,
		Subscriber: bcast,
		MaxRetries: 3,
	}

	// Phase 9.5: federation poller. Hits each registered child CP's
	// /api/v1/cp/introspect on a 30s tick, caches the response on the
	// federation_peers row. Started in Run() so we don't spin up a
	// background goroutine inside New() before the caller has a
	// chance to fail-stop.
	srv.fedPoller = federation.NewPoller(store, nil)
	srv.fedAgg = federation.NewAggregator(store)

	// Optional fleet auto-deployer: when --fleet-ssh-key-path is
	// set, the orchestrator can install the jobs runtime
	// unattended via SSH. Failure to load the key is non-fatal —
	// the CP boots without auto-deploy and operators install
	// manually from the Nodes UI.
	var autoDep api.AutoDeployer
	if cfg.FleetSSHKeyPath != "" {
		var binResolver sshdeploy.DaemonBinaryResolver
		if cfg.DaemonBinariesDir != "" {
			binResolver = api.NewDBBinaryResolver(store)
		}
		dep, derr := api.NewFleetAutoDeployer(store, srv, cfg.FleetSSHKeyPath, cfg.EffectiveMgmtURL(), cfg.DaemonBinaryPath, binResolver, cfg.FleetAnthropicAPIKey, cfg.FleetOpenAIAPIKey)
		if derr != nil {
			log.Printf("orchestrator auto-deploy disabled: %v", derr)
		} else if dep != nil {
			autoDep = dep
			log.Printf("orchestrator auto-deploy enabled: ssh-key=%s", cfg.FleetSSHKeyPath)
		}
	}

	// Orchestrator coordinator — needs fedAgg in scope so its
	// federatedDispatcher can resolve `cp: <child_id>` step targets.
	//
	// CPLocalEnvExtras forwards Fleet API keys into any cp-local
	// subprocess (cron orchestration steps with no node: target),
	// mirroring how the auto-deployer drops keys into
	// /etc/okesu/jobs.env on fleet nodes.
	//
	// CP API access from cp-local steps is handled declaratively now
	// via the orchestrator `data:` block + the api-package data
	// resolver — see data_resolver.go. The earlier "log in via curl
	// using OKESU_CP_ADMIN_PASSWORD" workaround has been removed.
	var cpLocalEnv []string
	if cfg.FleetAnthropicAPIKey != "" {
		cpLocalEnv = append(cpLocalEnv, "ANTHROPIC_API_KEY="+cfg.FleetAnthropicAPIKey)
	}
	if cfg.FleetOpenAIAPIKey != "" {
		cpLocalEnv = append(cpLocalEnv, "OPENAI_API_KEY="+cfg.FleetOpenAIAPIKey)
	}

	// Phase 22.4: IOC enrichment service. Adapters self-skip when their
	// API key is empty, so the service is harmless to construct on a CP
	// with no vendor keys configured — the enrich_ioc action then
	// returns an empty result rather than failing. The cache adapter
	// (api.enrichmentStoreAdapter) carries the operator-configured TTL
	// so each Upsert stamps a per-row expires_at.
	enrichmentAdapters := []enrichment.Enricher{}
	if cfg.Enrichment.VirusTotalAPIKey != "" {
		enrichmentAdapters = append(enrichmentAdapters, &enrichment.VirusTotal{APIKey: cfg.Enrichment.VirusTotalAPIKey})
	}
	if cfg.Enrichment.AbuseIPDBAPIKey != "" {
		enrichmentAdapters = append(enrichmentAdapters, &enrichment.AbuseIPDB{APIKey: cfg.Enrichment.AbuseIPDBAPIKey})
	}
	if cfg.Enrichment.ShodanAPIKey != "" {
		enrichmentAdapters = append(enrichmentAdapters, &enrichment.Shodan{APIKey: cfg.Enrichment.ShodanAPIKey})
	}
	enrichmentStore := api.NewEnrichmentStoreAdapter(store, cfg.Enrichment.DefaultTTL)
	enrichmentSvc := enrichment.New(enrichmentAdapters, enrichmentStore, cfg.Enrichment.DefaultTTL, cfg.Enrichment.RatePerSecond)

	srv.orchestra = api.NewOrchestrationCoordinator(store, srv.runs, srv.tunReg, cfg.AgentFilesDirs, srv.fedAgg, api.CoordinatorOpts{
		AutoDeployer:      autoDep,
		CPLocalEnvExtras:  cpLocalEnv,
		ActionPolicy:      orchestrator.Policy{AutoApprove: cfg.Policy.AutoApprove},
		EnrichmentService: enrichmentSvc,
	})

	// Wire the enrichment-trigger dispatch path. Each fresh cache
	// write (NOT cache hits — see EnrichedEvent doc) probes the
	// `ioc_enriched`-trigger orchestrations. Done after the
	// coordinator is constructed so OnEnrichment is bound.
	enrichmentSvc.SetEnrichedHook(func(e enrichment.EnrichedEvent) {
		srv.orchestra.OnEnrichment(orchestrator.EnrichmentPayload{
			IOCID:           e.IOCID,
			IOCKind:         e.IOCKind,
			NormalizedValue: e.NormalizedValue,
			Adapter:         e.Adapter,
			Verdict:         e.Verdict,
			Score:           e.Score,
		})
	})

	// Wire the finding-trigger hook before the pipeline starts so we
	// don't miss the first projected finding after boot.
	pipelineWorker.SetFindingHook(func(e eventpipeline.FindingProjectedEvent) {
		srv.orchestra.OnFinding(orchestrator.FindingPayload{
			ID:         e.FindingID,
			Severity:   e.Severity,
			Title:      e.Title,
			Agent:      e.Agent,
			Host:       e.Host,
			Category:   e.Category,
			Subtype:    e.Subtype,
			DedupKey:   e.DedupKey,
			Resource:   e.Resource,
			Attributes: e.Attributes,
		})
	})
	go func() {
		if err := pipelineWorker.Run(pipelineCtx); err != nil && pipelineCtx.Err() == nil {
			log.Printf("eventpipeline worker exited: %v", err)
		}
	}()

	// Federated finding fan-out (#155): poll each child CP's findings
	// and route newly-projected ones through the local OnFinding hook
	// so orchestrations installed on the parent fire for findings
	// projected anywhere in the federation. No-op when there are no
	// peers; cheap when there are.
	srv.fedFindingFanout = api.NewFederationFindingFanout(srv.fedAgg, srv.orchestra.OnFinding, 0)
	go srv.fedFindingFanout.Run(pipelineCtx)

	// Phase 4: OIDC. Optional — boot continues if discovery fails so the CP
	// stays available with password auth even when the IDP is unreachable.
	if cfg.OIDCEnabled() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		oidcProv, oerr := auth.NewOIDC(ctx, store, mgr, auth.OIDCConfig{
			Issuer:       cfg.OIDCIssuer,
			ClientID:     cfg.OIDCClientID,
			ClientSecret: cfg.OIDCClientSecret,
			RedirectURL:  cfg.OIDCRedirectURL,
			GroupsClaim:  cfg.OIDCGroupsClaim,
			RoleMap:      cfg.OIDCRoleMap,
		})
		if oerr != nil {
			log.Printf("oidc: disabled — %v", oerr)
		} else {
			srv.oidc = oidcProv
			log.Printf("oidc: enabled (issuer=%s)", cfg.OIDCIssuer)
		}
	}
	// Auto-import any okesu-<os>-<arch> files already in the binaries dir so
	// existing setups (tests, scripts that pre-stage binaries) don't have to
	// upload through the UI just to get a row in the inventory.
	if cfg.DaemonBinariesDir != "" {
		if err := importDaemonBinaries(store, cfg.DaemonBinariesDir); err != nil {
			log.Printf("daemon binaries import: %v (continuing)", err)
		}
	}

	// Phase 22: hydrate the iocs table from on-disk YAML catalog
	// directories. Runs synchronously (blocks startup) so an operator
	// sees the loaded count in boot logs before traffic flows. A
	// missing directory is not fatal — LoadDir returns empty and the
	// CP boots with whatever the dirs that DO exist contributed. The
	// SIGHUP handler in Run() re-runs this same path on demand so
	// catalog edits land without a full restart.
	loadIOCCatalogs(cfg.IOCCatalogDirs, store)

	// Initialize the mgmt-plane server FIRST so srv.mgmtHTTP is set when
	// srv.routes() captures the AboutFeatures snapshot.
	if cfg.MgmtListen != "" {
		srv.mgmtHTTP = &http.Server{
			Addr:              cfg.MgmtListen,
			Handler:           srv.mgmtRoutes(),
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       90 * time.Second,
		}
	}
	srv.http = &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0, // SSE needs unbounded write timeout
		IdleTimeout:       90 * time.Second,
	}
	return srv, nil
}

func (s *Server) routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)

	// Public webhook endpoint — auth via HMAC, not cookies.
	r.Post("/api/webhooks/events", api.WebhookHandler(s.queue, s.cfg.WebhookSecret, s.bcast))

	// External findings ingest — auth via Bearer API token, scope=findings:write.
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireToken(s.store, auth.ScopeFindingsWrite))
		r.Post("/api/findings/ingest", api.FindingIngest(s.store, s.eventStore, s.bcast))
	})

	// Federation introspect (Phase 9) — auth via X-Okesu-Federation-Token,
	// not session cookies. Returns this CP's identity + aggregate counts so
	// a parent CP can keep a federated view fresh without scraping internal
	// state. Mounted in the public router; auth happens inside the handler.
	// Phase 9.6: federation read endpoints. Token-authed siblings of
	// the local read endpoints — the parent CP fans out to these to
	// build merged Findings / Daimons / Nodes / Events views.
	r.Get("/api/v1/federation/findings", api.FederationFindings(s.store))
	r.Get("/api/v1/federation/findings/summary", api.FederationFindingsSummary(s.store))
	r.Get("/api/v1/federation/findings/grouped", api.FederationFindingsGrouped(s.store))
	r.Get("/api/v1/federation/findings/{id}", api.FederationFindingDetail(s.store))
	r.Get("/api/v1/federation/findings/{id}/runs", api.FederationRunsForFinding(s.store))
	r.Post("/api/v1/federation/findings/{id}/status", api.FederationFindingSetStatus(s.store))

	// Phase B: orchestration federation endpoints.
	r.Get("/api/v1/federation/orchestrations", api.FederationOrchestrationsList(s.store))
	r.Get("/api/v1/federation/orchestrations/{id}", api.FederationOrchestrationDetail(s.store))
	// Phase 22.6 — investigations federation read pipe. Detail is
	// child-scoped (workspace tabs are per-CP); parent UI uses ?cp=
	// proxy to navigate.
	r.Get("/api/v1/federation/investigations", api.FederationInvestigationsList(s.store))
	r.Get("/api/v1/federation/investigations/{id}", api.FederationInvestigationDetail(s.store))
	// Phase 22.7 — write proxies. Open-investigation from a federated
	// finding (FederationCreateInvestigation), add-to-existing
	// (FederationLinkFindingToInvestigation), and workspace-driven
	// run linking (FederationLinkRunToInvestigation) all go through
	// the same `?cp=<id>` proxy convention as the read paths.
	r.Post("/api/v1/federation/investigations", api.FederationCreateInvestigation(s.store))
	r.Put("/api/v1/federation/investigations/{id}/findings/{finding_id}", api.FederationLinkFindingToInvestigation(s.store))
	r.Put("/api/v1/federation/investigations/{id}/runs/{run_id}", api.FederationLinkRunToInvestigation(s.store))
	r.Post("/api/v1/federation/orchestrations", api.FederationOrchestrationCreate(s.store))
	r.Put("/api/v1/federation/orchestrations/{id}", api.FederationOrchestrationUpdate(s.store))
	r.Delete("/api/v1/federation/orchestrations/{id}", api.FederationOrchestrationDelete(s.store))
	r.Post("/api/v1/federation/orchestrations/{id}/run", api.FederationOrchestrationRunCreate(s.store, s.orchestra))
	r.Get("/api/v1/federation/orchestration-runs", api.FederationOrchestrationRunsList(s.store))
	r.Get("/api/v1/federation/orchestration-runs/{id}", api.FederationOrchestrationRunDetail(s.store))
	r.Post("/api/v1/federation/orchestration-runs/{id}/cancel", api.FederationOrchestrationRunCancel(s.store))
	r.Post("/api/v1/federation/orchestration-runs/bulk-cancel", api.FederationOrchestrationRunsBulkCancel(s.store))
	r.Post("/api/v1/federation/orchestration-runs/bulk-retry", api.FederationOrchestrationRunsBulkRetry(s.store, s.orchestra))
	r.Post("/api/v1/federation/orchestration-runs/{id}/steps/{stepID}/approve", api.FederationOrchestrationStepApprove(s.store, s.orchestra))
	r.Post("/api/v1/federation/runs/sync", api.FederationRunSync(s.runs, s.tunReg, s.store, s.cfg.AgentFilesDirs))
	r.Get("/api/v1/federation/daimons", api.FederationDaimons(s.store))
	r.Get("/api/v1/federation/daimons/{name}", api.FederationAgentDetail(s.store))
	r.Get("/api/v1/federation/nodes", api.FederationNodes(s.store))
	r.Get("/api/v1/federation/nodes/{id}", api.FederationNodeDetail(s.store))
	r.Get("/api/v1/federation/events", api.RequireFederationToken(s.store, api.EventsList(s.eventStore)))
	r.Get("/api/v1/federation/events/stream", api.RequireFederationToken(s.store, api.EventsStream(s.bcast)))
	r.Get("/api/v1/federation/insights/findings", api.FederationInsightsFindings(s.store))
	r.Get("/api/v1/federation/insights/events", api.RequireFederationToken(s.store, api.InsightsEvents(s.eventStore)))

	// Phase 9.7: federation writes. Token-authed POST endpoints the
	// parent's forwarding handlers proxy to when an operator picks a
	// target child CP from the Global UI.
	r.Post("/api/v1/federation/nodes", api.FederationNodeCreate(s.store))
	r.Post("/api/v1/federation/nodes/{id}/deploy", api.FederationNodeDeploy(s.store, s.jobs, s, api.NodesConfig{
		DaemonBinaryPath:  s.cfg.DaemonBinaryPath,
		DaemonBinariesDir: s.cfg.DaemonBinariesDir,
		DaimonFilesDir:    s.cfg.DaimonFilesDir,
		WebhookSecret:     s.cfg.WebhookSecret,
		WebhookURL:        s.cfg.EffectiveWebhookURL(),
		MgmtURL:           s.cfg.EffectiveMgmtURL(),
		Secrets:           s.secrets,
	}))
	r.Get("/api/v1/federation/runs", api.FederationRunsList(s.store))
	r.Get("/api/v1/federation/runs/{id}", api.FederationRunStatus(s.runs, s.store))
	r.Post("/api/v1/federation/runs", api.FederationCreateRun(s.runs, s.tunReg, s.store, s.cfg.AgentFilesDirs))
	r.Post("/api/v1/federation/runs/{id}/cancel", api.FederationCancelRun(s.runs, s.tunReg, s.store))

	r.Get("/api/v1/cp/introspect", api.CPIntrospect(api.CPIntrospectDepsValue{
		Store:           s.store,
		Version:         Version(),
		DaemonVersionFn: s.daemonBinaryVersion,
		Features: api.AboutFeatures{
			OIDC:          s.oidc != nil,
			MgmtPlane:     s.cfg.MgmtListen != "",
			Tunnel:        s.tunReg != nil,
			Deploy:        s.cfg.DaemonBinaryPath != "" && s.cfg.DaimonFilesDir != "",
			WebhookIngest: s.cfg.WebhookSecret != "",
		},
		WebhookURL: s.cfg.EffectiveWebhookURL(),
		MgmtURL:    s.cfg.EffectiveMgmtURL(),
	}))

	// Phase 21.1 — child CPs call this exactly once with the
	// bootstrap token from their bundle. Public on purpose: the
	// token is the auth, and after this single exchange it's burned.
	r.Post("/api/v1/cp/bootstrap", api.CPBootstrapHandler(s.store, s.fedPoller, s.bundleCache))
	// Phase 21.3b — bundle download for managed deploys. The
	// cloud-init script in the launched VM uses Bearer auth via
	// the bootstrap token to fetch the cached tar.gz. Public on
	// the UI port (no cookie auth needed; the token is the auth).
	r.Get("/api/federation/cp-bundle/download", api.CPBundleDownloadHandler(s.store, s.bundleCache))

	// Public auth endpoints.
	r.Post("/api/auth/login", api.LoginHandler(s.store, s.mgr))
	r.Get("/api/auth/config", api.AuthConfigHandler(s.oidc != nil, s.cfg.OIDCLabel))

	// OIDC sign-in (only registered when configured).
	if s.oidc != nil {
		r.Get("/auth/oidc/login", s.oidc.LoginHandler)
		r.Get("/auth/oidc/callback", s.oidc.CallbackHandler)
	}

	// Authenticated API — read paths require any logged-in user (viewer+).
	r.Group(func(r chi.Router) {
		r.Use(s.mgr.Middleware)
		r.Post("/api/auth/logout", api.LogoutHandler(s.store, s.mgr))
		r.Get("/api/auth/me", api.MeHandler())

		// Self-service endpoints — every authenticated user
		r.Post("/api/users/me/password", api.MyPasswordChange(s.store))
		r.Get("/api/users/me/sessions", api.MySessions(s.store, s.mgr))
		r.Delete("/api/users/me/sessions", api.MyRevokeOtherSessions(s.store, s.mgr))
		r.Get("/api/dashboard", api.Dashboard(s.store, s.eventStore, s.tunReg, s.cfg.DaimonFilesDir, s.daemonBinaryVersion))
		r.Get("/api/insights/findings", api.FederatedInsightsFindings(s.store, s.fedAgg))
		r.Get("/api/insights/events", api.FederatedInsightsEvents(api.InsightsEvents(s.eventStore), s.fedAgg))
		r.Get("/api/insights/triage-outcomes", api.InsightsTriageOutcomes(s.store))
		r.Get("/api/insights/orchestrations-top", api.InsightsOrchestrationsTop(s.store))
		r.Get("/api/system/about", api.AboutHandler(Version(), s.daemonBinaryVersion, api.AboutFeatures{
			OIDC:          s.oidc != nil,
			MgmtPlane:     s.mgmtHTTP != nil,
			Tunnel:        s.mgmtHTTP != nil,
			Deploy:        s.cfg.DaemonBinaryPath != "" && s.cfg.DaimonFilesDir != "",
			WebhookIngest: s.cfg.WebhookSecret != "",
		}))

		// Read endpoints — viewer+. List endpoints route through the
		// federation aggregator so the parent's UI shows merged
		// (local + federated) rows when peers are registered. Detail
		// endpoints stay local-only — drilling into a specific
		// finding/agent/node by id is always a local concern.
		r.Get("/api/events", api.FederatedEventsList(api.EventsList(s.eventStore), s.fedAgg))
		r.Get("/api/events/stream", api.FederatedEventsStream(s.bcast, s.fedAgg))
		r.Get("/api/agents", api.FederatedAgentsList(s.store, s.fedAgg))
		r.Get("/api/agents/{name}", api.FederatedAgentDetail(s.store, s.fedAgg))
		r.Get("/api/findings", api.FederatedFindingsList(s.store, s.fedAgg))
		r.Get("/api/findings/summary", api.FederatedFindingsSummary(s.store, s.fedAgg))
		r.Get("/api/findings/grouped", api.FederatedFindingsGrouped(s.store, s.fedAgg))
		// Phase 22.3 — active war-bridge feed for the dashboard red banner.
		// Static segment must register before /api/findings/{id} so chi
		// doesn't try to ParseInt "war-bridge". Local-only for now.
		r.Get("/api/findings/war-bridge", api.ListActiveWarBridgeFindingsHandler(s.store))
		r.Get("/api/findings/{id}", api.FederatedFindingDetail(s.store, s.fedAgg))
		r.Get("/api/findings/{id}/runs", api.FederatedRunsForFinding(s.store, s.fedAgg))
		// Phase 22.6 — case membership lookup. Used by the
		// t2-hypothesis-test orchestration to discover which cases
		// need a verdict note + by the UI's finding-detail panel.
		r.Get("/api/findings/{id}/investigations", api.ListInvestigationsForFindingHandler(s.store))

		// Phase 22.1 — IOC list. Filter by finding_id (drawer drill-down)
		// or kind (e.g. all observed sha256s). Local-only for now.
		r.Get("/api/iocs", api.ListIOCs(s.store))
		// Phase 22.4 — cross-CP IOC pattern rollup. Drives the
		// cross-cp-ioc-pattern-supervisor daimon. Static segment must
		// register before any future /api/iocs/{id} catch-all so chi
		// doesn't try to ParseInt "cross-cp-patterns".
		r.Get("/api/iocs/cross-cp-patterns", api.ListCrossCPPatternsHandler(s.store))
		// Phase 22.B — single IOC record fetch (drawer / deep-link).
		r.Get("/api/iocs/{id}", api.GetIOCHandler(s.store))
		// Phase 22.4 — typed relationship edges for a single IOC (both
		// directions). Sub-path must register before any /api/iocs/{id}
		// catch-all so chi routes it correctly.
		r.Get("/api/iocs/{id}/relationships", api.ListIOCRelationshipsHandler(s.store))
		// Phase 22.C — observation history for a single IOC (Catalog detail Observations tab).
		r.Get("/api/iocs/{id}/observations", api.ListIOCObservationsHandler(s.store))
		// Phase 22.4 — STIX 2.1 bundle export. Supports ?kind= and ?since= filters.
		r.Get("/api/stix2/iocs", api.STIX2ExportHandler(s.store))
		// Phase 22.5 — YARA rule bundle export. Concatenates all yara_rule
		// IOCs into a single .yar file the binary-analyzer agent feeds to
		// the system `yara` CLI. Optional ?tag= filter.
		r.Get("/api/catalog/yara-rules.yar", api.YARARulesYarHandler(s.store))

		// Phase 22.3 — Investigations (T2 case workspace). CRUD plus
		// notes and finding linking; viewer+ for now (no admin gate)
		// since cases are operator workflow, not config.
		// Phase 22.6 — investigations now federate. The list merges
		// local + each child's, tagging remotes with cp_source. Detail
		// proxies via ?cp= for remotes; falls through to local
		// otherwise.
		r.Get("/api/investigations", api.FederatedInvestigationsList(s.store, s.fedAgg))
		r.Post("/api/investigations", api.FederatedCreateInvestigation(s.store, s.fedAgg))
		r.Post("/api/investigations/by-dedup", api.UpsertInvestigationByDedupHandler(s.store))
		r.Get("/api/investigations/{id}", api.FederatedInvestigationDetail(s.store, s.fedAgg))
		r.Patch("/api/investigations/{id}", api.UpdateInvestigationHandler(s.store))
		r.Post("/api/investigations/{id}/notes", api.AddInvestigationNoteHandler(s.store))
		r.Put("/api/investigations/{id}/findings/{finding_id}", api.FederatedLinkFindingToInvestigation(s.store, s.fedAgg))
		r.Delete("/api/investigations/{id}/findings/{finding_id}", api.UnlinkFindingFromInvestigationHandler(s.store))
		r.Put("/api/investigations/{id}/runs/{run_id}", api.FederatedLinkRunToInvestigation(s.store, s.fedAgg))
		r.Delete("/api/investigations/{id}/runs/{run_id}", api.UnlinkRunFromInvestigationHandler(s.store))

		// Read endpoints (continued)
		r.Get("/api/nodes", api.FederatedNodesList(s.store, s.fedAgg))
		r.Get("/api/nodes/{id}", api.FederatedNodeDetail(s.store, s.fedAgg))
		r.Get("/api/nodes/library", api.AgentLibrary(api.NodesConfig{
			DaemonBinaryPath:  s.cfg.DaemonBinaryPath,
			DaemonBinariesDir: s.cfg.DaemonBinariesDir,
			DaimonFilesDir:    s.cfg.DaimonFilesDir,
		}))
		r.Get("/api/daimons/library", api.DaimonLibraryList(s.cfg.DaimonFilesDir))
		r.Get("/api/daimons/library/{name}", api.DaimonLibraryGet(s.cfg.DaimonFilesDir))
		r.Get("/api/agent-library", api.AgentLibraryList(s.cfg.AgentFilesDirs))
		r.Get("/api/agent-library/{name}", api.AgentLibraryGet(s.cfg.AgentFilesDirs))
		r.Get("/api/deploy/binaries", api.BinariesList(s.store, s.cfg.DaemonBinariesDir))
		r.Get("/api/deploy/known-hosts", api.KnownHostsList(s.store))
		r.Get("/api/nodes/{id}/known-host", api.NodeKnownHost(s.store))

		// Notifications: deliveries log is viewer+; channels/rules are admin-only.
		r.Get("/api/notifications/deliveries", api.DeliveriesList(s.store))
		r.Get("/api/nodes/connected", api.ConnectedNodes(s.tunReg))
		r.Get("/api/jobs/{id}", api.JobStatus(s.jobs))
		r.Get("/api/jobs/{id}/log", api.JobLogStream(s.jobs))
		r.Get("/api/runs", api.FederatedRunsList(s.store, s.fedAgg))
		r.Get("/api/runs/{id}", api.FederatedRunStatus(s.runs, s.store, s.fedAgg))
		r.Get("/api/runs/{id}/log", api.RunLogStream(s.runs, s.store))

		// Admin-only endpoints
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireRole(auth.RoleAdmin))
			r.Get("/api/users", api.UsersList(s.store))
			r.Post("/api/users", api.UserCreate(s.store))
			r.Get("/api/users/{id}", api.UserDetail(s.store))
			r.Patch("/api/users/{id}", api.UserPatch(s.store))
			r.Delete("/api/users/{id}", api.UserDelete(s.store))
			r.Get("/api/audit", api.AuditList(s.store))
			r.Post("/api/deploy/binaries", api.BinaryUpload(s.store, s.cfg.DaemonBinariesDir))
			r.Delete("/api/deploy/binaries/{name}", api.BinaryDelete(s.store))
			r.Delete("/api/nodes/{id}/known-host", api.NodeKnownHostDelete(s.store))

			r.Get("/api/notifications/channels", api.ChannelsList(s.store))
			r.Post("/api/notifications/channels", api.ChannelCreate(s.store))
			r.Patch("/api/notifications/channels/{id}", api.ChannelPatch(s.store))
			r.Delete("/api/notifications/channels/{id}", api.ChannelDelete(s.store))
			r.Post("/api/notifications/channels/{id}/test", api.ChannelTest(s.store, s.notify))
			r.Get("/api/notifications/rules", api.RulesList(s.store))
			r.Post("/api/notifications/rules", api.RuleCreate(s.store))
			r.Patch("/api/notifications/rules/{id}", api.RulePatch(s.store))
			r.Delete("/api/notifications/rules/{id}", api.RuleDelete(s.store))

			r.Get("/api/tokens", api.TokensList(s.store))
			r.Post("/api/tokens", api.TokenCreate(s.store))
			r.Delete("/api/tokens/{id}", api.TokenRevoke(s.store))

			// Phase 21.2 — cloud credentials. Admin-only because the
			// payloads are encrypted secrets that, once decrypted,
			// authorise spending on the operator's cloud account.
			r.Get("/api/cloud-credentials", api.CloudCredentialsList(s.store))
			r.Post("/api/cloud-credentials", api.CloudCredentialCreate(s.store))
			// In-place edit. Partial: {name?, region?, payload?}. Secret
			// fields stay intact unless the operator re-types them.
			r.Put("/api/cloud-credentials/{id}", api.CloudCredentialUpdate(s.store))
			r.Delete("/api/cloud-credentials/{id}", api.CloudCredentialDelete(s.store))
			r.Post("/api/cloud-credentials/{id}/test", api.CloudCredentialTest(s.store))
			// Phase 21.5 — per-credential monthly USD budget. Empty body
			// (or {"monthly_budget_usd": null}) clears the cap.
			r.Put("/api/cloud-credentials/{id}/budget", api.CloudCredentialBudgetUpdate(s.store))
			// Cloud-side discovery for the +Add CP "Managed deploy"
			// dropdowns. Each handler decrypts the credential, calls
			// the relevant SDK list endpoint, and returns
			// {id, name, attrs}[] so the UI can build a select.
			r.Get("/api/cloud-credentials/{id}/oci/compartments", api.CloudDiscoveryOCICompartments(s.store))
			r.Get("/api/cloud-credentials/{id}/oci/availability-domains", api.CloudDiscoveryOCIAvailabilityDomains(s.store))
			r.Get("/api/cloud-credentials/{id}/oci/subnets", api.CloudDiscoveryOCISubnets(s.store))
			r.Get("/api/cloud-credentials/{id}/oci/images", api.CloudDiscoveryOCIImages(s.store))
			r.Get("/api/cloud-credentials/{id}/oci/shapes", api.CloudDiscoveryOCIShapes(s.store))

			// Phase 9.5: federation peers — admin-only because adding a
			// peer means storing a credential for an outbound CP.
			r.Get("/api/federation/peers", api.FederationListPeers(s.store))
			r.Post("/api/federation/peers", api.FederationAddPeer(s.store, s.fedPoller))
			// Phase A — register an S3-dead-drop child CP. Operator
			// supplies a transport_config (the bucket coords + creds
			// the parent will read with) + the bucket prefix the
			// child publishes to. No synchronous probe — the bucket
			// may legitimately be empty until the child's first tick.
			r.Post("/api/federation/peers/s3", api.FederationAddS3Peer(s.store))
			r.Delete("/api/federation/peers/{id}", api.FederationDeletePeer(s.store))
			r.Post("/api/federation/peers/{id}/refresh", api.FederationRefreshPeer(s.store, s.fedPoller))
			// Phase 21.1 — generate a bootstrap bundle for a new
			// child CP. Admin-only because the response embeds a
			// one-time bootstrap token + a fresh admin password.
			r.Post("/api/federation/cp-bundle", api.CPBundleHandler(s.store, api.CPBundleConfig{
				// Bootstrap target is the UI port — that's where
				// /api/v1/cp/bootstrap lives. Mgmt port is mTLS-only
				// and the new child has no client cert yet.
				ParentMgmtURL:     s.cfg.EffectivePublicURL(),
				LinuxBinaryPath:   s.cfg.CPBootstrapBinaryPath,
				LinuxImageTarPath: s.cfg.CPBootstrapImageTarPath,
				Version:           Version(),
			}, s.bundleCache, s.cfg.EffectivePublicURL()))

			// Phase 21.3 — managed CP provisioning. Admin-only.
			// The Provisioner registry is populated by per-cloud
			// impls registered in server.New() below — OCI ships in
			// 21.3b, AWS in 21.3c, etc.
			r.Get("/api/federation/cp-provisioners", api.CPProvisionersListHandler(s.cpProvisioners))
			r.Post("/api/federation/cp-provision", api.CPProvisionCreateHandler(
				s.store, s.cpProvisioners, s.cfg.EffectivePublicURL(),
				api.CPProvisionWorkerConfig{
					Store:    s.store,
					Registry: s.cpProvisioners,
					Cache:    s.bundleCache,
					Bundle: api.CPBundleConfig{
						ParentMgmtURL:     s.cfg.EffectivePublicURL(),
						LinuxBinaryPath:   s.cfg.CPBootstrapBinaryPath,
						LinuxImageTarPath: s.cfg.CPBootstrapImageTarPath,
						Version:           Version(),
					},
					ParentBaseURL: s.cfg.EffectivePublicURL(),
				},
			))
			r.Get("/api/federation/cp-provisions", api.CPProvisionsListHandler(s.store))
			r.Get("/api/federation/cp-provisions/{id}", api.CPProvisionGetHandler(s.store))
			// Delete the row. Optional `?destroy=true` also tries to
			// terminate the cloud-side instance via Provisioner.Destroy.
			// Without that flag the cloud resource is left alone, which
			// is the safe default for the common "clean up failed rows"
			// case where there's no live instance to kill.
			r.Delete("/api/federation/cp-provisions/{id}", api.CPProvisionDeleteHandler(s.store, s.cpProvisioners))
			// Phase 21.5 — read-only cost preview the +Add CP modal
			// hits on every form change. Same body shape as the
			// create endpoint; never mints tokens or inserts rows.
			r.Post("/api/federation/cp-provision/estimate", api.CPProvisionEstimateHandler(s.store))

			// System / database (admin)
			r.Get("/api/system/db/stats", api.DBStats(s.store, api.SystemDBConfig{
				EventTTLDays: s.cfg.EventTTLDays,
			}))
			r.Post("/api/system/db/vacuum", api.DBVacuum(s.store))
			r.Post("/api/system/db/prune-events", api.DBPruneEvents(s.store))
		})

		// Mutation endpoints — operator+
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireRole(auth.RoleOperator))
			r.Patch("/api/agents/{name}/config", api.AgentConfigUpdate(s.store))
			r.Post("/api/findings/{id}/acknowledge", api.FindingAcknowledge(s.store))
			r.Post("/api/findings/group/acknowledge", api.FindingsGroupAcknowledge(s.store))
			r.Post("/api/findings/{id}/status", api.FederatedFindingSetStatus(s.store, s.fedAgg))
			r.Post("/api/findings/group/status", api.FindingsGroupSetStatus(s.store))
			r.Put("/api/daimons/library/{name}", api.DaimonLibraryPut(s.store, s.cfg.DaimonFilesDir))
			r.Post("/api/daimons/library/{name}/rollback", api.DaimonLibraryRollback(s.store, s.cfg.DaimonFilesDir))
			r.Delete("/api/daimons/library/{name}", api.DaimonLibraryDelete(s.store, s.cfg.DaimonFilesDir))
			r.Put("/api/agent-library/{name}", api.AgentLibraryPut(s.store, s.cfg.AgentFilesDirs))
			r.Delete("/api/agent-library/{name}", api.AgentLibraryDelete(s.store, s.cfg.AgentFilesDirs))
			r.Post("/api/findings/{id}/severity", api.FindingSetSeverity(s.store))
			r.Get("/api/findings/severity-rules", api.SeverityRulesList(s.store))
			r.Delete("/api/findings/severity-rules", api.SeverityRuleDelete(s.store))
			r.Post("/api/nodes", api.ForwardingNodeCreate(s.store, s.fedAgg))
			r.Delete("/api/nodes/{id}", api.NodeDelete(s.store))
			r.Post("/api/nodes/{id}/refresh-metadata", api.NodeRefreshMetadata(s.store, s.tunReg))
			r.Put("/api/nodes/{id}/auto-update", api.NodeAutoUpdateToggle(s.store))
			r.Get("/api/system/deploy-ssh-key", api.DeployKeyGet(s.secrets))
			r.Put("/api/system/deploy-ssh-key", api.DeployKeyPut(s.store, s.secrets))
			r.Delete("/api/system/deploy-ssh-key", api.DeployKeyDelete(s.store, s.secrets))
			r.Post("/api/nodes/{id}/update-binary", api.NodeUpdateBinary(s.store, s.jobs, api.NodesConfig{
				DaemonBinaryPath:  s.cfg.DaemonBinaryPath,
				DaemonBinariesDir: s.cfg.DaemonBinariesDir,
				Secrets:           s.secrets,
			}))
			r.Post("/api/nodes/{id}/rollback-binary", api.NodeRollbackBinary(s.store, s.jobs, api.NodesConfig{
				Secrets: s.secrets,
			}))
			r.Post("/api/nodes/{id}/deploy", api.ForwardingNodeDeploy(s.store, s.jobs, s, api.NodesConfig{
				DaemonBinaryPath:  s.cfg.DaemonBinaryPath,
				DaemonBinariesDir: s.cfg.DaemonBinariesDir,
				DaimonFilesDir:    s.cfg.DaimonFilesDir,
				WebhookSecret:     s.cfg.WebhookSecret,
				WebhookURL:        s.cfg.EffectiveWebhookURL(),
				MgmtURL:           s.cfg.EffectiveMgmtURL(),
				Secrets:           s.secrets,
			}, s.fedAgg))
			// Phase 4: install the host-side jobs runtime on a node.
			// Operator-triggered (via the Nodes UI) — body carries the
			// SSH credential, the CP issues a fresh node-cert and runs
			// the SSH install in a background job.
			r.Post("/api/nodes/{id}/install-jobs-runtime", api.InstallJobsRuntime(s.store, s.jobs, s, func() (string, string, sshdeploy.DaemonBinaryResolver) {
				var binResolver sshdeploy.DaemonBinaryResolver
				if s.cfg.DaemonBinariesDir != "" {
					binResolver = api.NewDBBinaryResolver(s.store)
				}
				return s.cfg.EffectiveMgmtURL(), s.cfg.DaemonBinaryPath, binResolver
			}))
			r.Post("/api/runs", api.ForwardingCreateRun(s.runs, s.tunReg, s.store, s.cfg.AgentFilesDirs, s.fedAgg))
			r.Post("/api/runs/{id}/cancel", api.ForwardingCancelRun(s.runs, s.tunReg, s.store, s.fedAgg))

			// Phase 9: S3 dead-drop transport — operators manage
			// bucket credentials + fleet keypairs via transport-configs,
			// and mint enrollment packages that auto-register N nodes.
			// Phase 11.4: finding history (audit trail) + run↔finding
			// linkage. Read-only — writes flow through the action
			// dispatcher or the existing triage handler.
			r.Get("/api/findings/{id}/history", api.FindingHistory(s.store))
			r.Get("/api/findings/{id}/runs", api.FindingLinkedRuns(s.store))
			r.Get("/api/orchestration-runs/{id}/findings", api.RunLinkedFindings(s.store))

			r.Get("/api/transport-configs", api.TransportConfigsList(s.store))
			r.Get("/api/transport-configs/{id}", api.TransportConfigDetail(s.store))
			r.Post("/api/transport-configs", api.TransportConfigCreate(s.store))
			r.Put("/api/transport-configs/{id}", api.TransportConfigUpdate(s.store))
			r.Delete("/api/transport-configs/{id}", api.TransportConfigDelete(s.store))
			r.Get("/api/enrollment-packages", api.EnrollmentPackagesList(s.store))
			r.Post("/api/enrollment-packages", api.EnrollmentPackageCreate(s.store))
			r.Get("/api/enrollment-packages/{id}/download", api.EnrollmentPackageDownload(s.store, func(target string) ([]byte, error) {
				// Look up via daemon_binaries by os/arch. Falls back
				// to the configured DaemonBinaryPath when the multi-
				// arch resolver has nothing for the target.
				return resolvePackageBinary(s, target)
			}))
			r.Post("/api/enrollment-packages/{id}/revoke", api.EnrollmentPackageRevoke(s.store))

			// Orchestrations (Phase A + B — local + federated).
			r.Get("/api/orchestrations", api.FederatedOrchestrationsList(s.store, s.fedAgg))
			r.Get("/api/orchestrations/{id}", api.FederatedOrchestrationDetail(s.store, s.fedAgg))
			r.Post("/api/orchestrations", api.FederatedOrchestrationCreate(s.store, s.fedAgg))
			r.Put("/api/orchestrations/{id}", api.FederatedOrchestrationUpdate(s.store, s.fedAgg))
			r.Delete("/api/orchestrations/{id}", api.FederatedOrchestrationDelete(s.store, s.fedAgg))
			r.Post("/api/orchestrations/{id}/run", api.FederatedOrchestrationRunCreate(s.store, s.orchestra, s.fedAgg))

			r.Get("/api/orchestration-runs", api.FederatedOrchestrationRunsList(s.store, s.fedAgg))
			r.Get("/api/orchestration-runs/{id}", api.FederatedOrchestrationRunDetail(s.store, s.fedAgg))
			r.Post("/api/orchestration-runs/{id}/cancel", api.FederatedOrchestrationRunCancel(s.store, s.fedAgg))
			r.Post("/api/orchestration-runs/bulk-cancel", api.FederatedOrchestrationRunsBulkCancel(s.store, s.fedAgg))
			r.Post("/api/orchestration-runs/bulk-retry", api.FederatedOrchestrationRunsBulkRetry(s.store, s.orchestra, s.fedAgg))
			r.Post("/api/orchestration-runs/{id}/steps/{stepID}/approve", api.FederatedOrchestrationStepApprove(s.store, s.orchestra, s.fedAgg))
		})
	})

	// UI: serve the embedded React build (or placeholder) for everything else.
	r.NotFound(ui.Handler().ServeHTTP)

	return r
}

// resolvePackageBinary returns the daemon binary bytes for a given
// "<os>-<arch>" target. Used by the package download endpoint to
// embed multi-arch okesu binaries in the generated archive.
//
// Resolution order:
//  1. daemon_binaries table (operator uploaded one for the os/arch)
//  2. configured DaemonBinaryPath when target matches local arch
//  3. nothing — caller surfaces a 412 to the operator
func resolvePackageBinary(s *Server, target string) ([]byte, error) {
	osName, archName := splitTargetTriple(target)
	if osName == "" || archName == "" {
		return nil, fmt.Errorf("bad target %q (want os-arch)", target)
	}
	if s.cfg.DaemonBinariesDir != "" {
		resolver := api.NewDBBinaryResolver(s.store)
		path, err := resolver.Resolve(osName, archName)
		if err == nil && path != "" {
			return os.ReadFile(path)
		}
	}
	// Fallback: local default binary, only if it matches the request.
	if s.cfg.DaemonBinaryPath != "" && osName == runtime.GOOS && archName == runtime.GOARCH {
		return os.ReadFile(s.cfg.DaemonBinaryPath)
	}
	return nil, nil
}

// startS3Scanner spins up one s3scanner.Scanner against a transport
// config. Forwards events into the existing eventpipeline + findings
// startFederationS3Publisher starts the child-side S3 publisher
// (Phase A) — writes this CP's introspect snapshot to the configured
// bucket prefix every 30s. The parent's s3reader picks it up.
//
// Two paths to bucket coords:
//
//   1. Inline (Phase A.1) — preferred when --federation-s3-publish-bucket
//      etc. are set. Used by the enrollment bundle which bakes everything
//      into env-vars so first boot doesn't need an existing
//      transport_config row.
//   2. By transport_config_id — preferred for operators with one bucket
//      shared across nodes + federation. Reuses the existing table.
//
// Errors here are logged + the publisher is skipped — a missing config
// or unreachable bucket should never crash the CP.
func (s *Server) startFederationS3Publisher(ctx context.Context) {
	cfg, err := s.federationPublisherConfig(ctx)
	if err != nil {
		log.Printf("federation s3 publisher: %v", err)
		return
	}
	// Phase A.2/A.3 — extra assets the publisher writes alongside
	// introspect.json. Each maps 1:1 with what the matching
	// /api/v1/federation/* endpoint emits over HTTPS, so the
	// aggregator's cached read returns the same wire shape.
	assets := []s3publisher.Asset{
		{Path: "findings.json", Render: s.renderFederationFindingsJSON},
		{Path: "daimons.json", Render: s.renderFederationDaimonsJSON},
		{Path: "nodes.json", Render: s.renderFederationNodesJSON},
		{Path: "orchestrations.json", Render: s.renderFederationOrchestrationsJSON},
		{Path: "investigations.json", Render: s.renderFederationInvestigationsJSON},
	}
	pub, err := s3publisher.New(ctx, *cfg, s.renderIntrospectJSON, assets...)
	if err != nil {
		log.Printf("federation s3 publisher: connect: %v", err)
		return
	}
	go pub.Run(ctx)
	log.Printf("federation s3 publisher: writing to %s/%s every 30s (introspect + %d extra asset(s))",
		cfg.Bucket, cfg.BucketPrefix, len(assets))
}

// startFederationS3Dispatcher starts the child-side write-pipe server
// (Phase B). Polls cp/*/outbound/<self>/req/*.json for directives the
// parent CP submitted, dispatches into the local HTTP handlers via the
// s3rpc bridge, and writes the response back to
// cp/<self>/outbound/<parent>/resp/<id>.json.
//
// Same enable gate as the publisher — if the bucket isn't configured,
// the dispatcher silently skips (an offline bucket should never crash
// the CP). The s3transport.Client used here is freshly built rather
// than shared with the publisher because the publisher's client is
// internal to that goroutine; the costs of one extra connect at boot
// are negligible.
func (s *Server) startFederationS3Dispatcher(ctx context.Context) {
	cfg, err := s.federationPublisherConfig(ctx)
	if err != nil {
		log.Printf("federation s3 dispatcher: %v", err)
		return
	}
	cli, err := s3transport.NewClient(ctx, s3transport.ClientConfig{
		Bucket:    cfg.Bucket,
		Endpoint:  cfg.Endpoint,
		Region:    cfg.Region,
		UseSSL:    cfg.UseSSL,
		AccessKey: cfg.AccessKey,
		SecretKey: cfg.SecretKey,
	})
	if err != nil {
		log.Printf("federation s3 dispatcher: connect: %v", err)
		return
	}
	meta, err := s.store.CPMeta()
	if err != nil {
		log.Printf("federation s3 dispatcher: cp_meta: %v", err)
		return
	}
	if meta.InstanceID == "" {
		log.Printf("federation s3 dispatcher: cp_meta has no instance_id (run --cp-instance-id on first boot)")
		return
	}
	srv, err := s3rpc.New(cli, meta.InstanceID)
	if err != nil {
		log.Printf("federation s3 dispatcher: %v", err)
		return
	}
	srv.Register(s3rpc.KindCreateNode, api.NewS3CreateNodeHandler(s.store))
	srv.Register(s3rpc.KindDeployDaimon,
		api.NewS3DeployDaimonHandler(s.store, s.jobs, s, s.deployNodesConfig()))
	srv.Register(s3rpc.KindCreateRun,
		api.NewS3CreateRunHandler(s.runs, s.tunReg, s.store, s.cfg.AgentFilesDirs))
	srv.Register(s3rpc.KindCancelRun,
		api.NewS3CancelRunHandler(s.runs, s.tunReg, s.store))
	srv.Register(s3rpc.KindFindingSetStatus,
		api.NewS3FindingSetStatusHandler(s.store))
	srv.Register(s3rpc.KindOrchestrationCreate,
		api.NewS3OrchestrationCreateHandler(s.store))
	srv.Register(s3rpc.KindOrchestrationUpdate,
		api.NewS3OrchestrationUpdateHandler(s.store))
	srv.Register(s3rpc.KindOrchestrationDelete,
		api.NewS3OrchestrationDeleteHandler(s.store))
	srv.Register(s3rpc.KindOrchestrationRunCreate,
		api.NewS3OrchestrationRunCreateHandler(s.store, s.orchestra))
	srv.Register(s3rpc.KindOrchestrationRunCancel,
		api.NewS3OrchestrationRunCancelHandler(s.store))
	srv.Register(s3rpc.KindOrchestrationStepApprove,
		api.NewS3OrchestrationStepApproveHandler(s.store, s.orchestra))
	srv.Register(s3rpc.KindOrchestrationRunsBulkCnl,
		api.NewS3OrchestrationRunsBulkCancelHandler(s.store))
	srv.Register(s3rpc.KindOrchestrationRunsBulkRetry,
		api.NewS3OrchestrationRunsBulkRetryHandler(s.store, s.orchestra))
	go srv.Run(ctx)
	log.Printf("federation s3 dispatcher: polling cp/*/outbound/%s/req/ every %s (13 kinds registered)",
		meta.InstanceID, s3rpc.DefaultServerPollInterval)
}

// deployNodesConfig assembles the api.NodesConfig used by deploy and
// install handlers. Factored out so the s3 dispatcher and the HTTP
// route registration share one definition.
func (s *Server) deployNodesConfig() api.NodesConfig {
	return api.NodesConfig{
		DaemonBinaryPath:  s.cfg.DaemonBinaryPath,
		DaemonBinariesDir: s.cfg.DaemonBinariesDir,
		DaimonFilesDir:    s.cfg.DaimonFilesDir,
		WebhookSecret:     s.cfg.WebhookSecret,
		WebhookURL:        s.cfg.EffectiveWebhookURL(),
		MgmtURL:           s.cfg.EffectiveMgmtURL(),
		Secrets:           s.secrets,
	}
}

// federationPublisherConfig assembles the s3publisher.Config from
// either inline flags (preferred when set) or a transport_config row.
// Returns an error if neither path has all required fields.
func (s *Server) federationPublisherConfig(ctx context.Context) (*s3publisher.Config, error) {
	prefix := s.cfg.FederationS3PublishPrefix
	if prefix == "" {
		return nil, fmt.Errorf("FederationS3PublishPrefix is empty")
	}

	// Path 1: inline coords. Need bucket + endpoint + access_key +
	// secret_key at minimum; region/use_ssl have sensible defaults.
	if s.cfg.FederationS3PublishBucket != "" &&
		s.cfg.FederationS3PublishEndpoint != "" &&
		s.cfg.FederationS3PublishAccessKey != "" &&
		s.cfg.FederationS3PublishSecretKey != "" {
		return &s3publisher.Config{
			Bucket:       s.cfg.FederationS3PublishBucket,
			Endpoint:     s.cfg.FederationS3PublishEndpoint,
			Region:       s.cfg.FederationS3PublishRegion,
			UseSSL:       s.cfg.FederationS3PublishUseSSL,
			AccessKey:    s.cfg.FederationS3PublishAccessKey,
			SecretKey:    s.cfg.FederationS3PublishSecretKey,
			BucketPrefix: prefix,
		}, nil
	}

	// Path 2: transport_config row.
	if s.cfg.FederationS3PublishConfigID == 0 {
		return nil, fmt.Errorf("neither inline bucket coords nor FederationS3PublishConfigID set")
	}
	tc, err := s.store.GetTransportConfig(s.cfg.FederationS3PublishConfigID)
	if err != nil {
		return nil, fmt.Errorf("transport_config %d: %w", s.cfg.FederationS3PublishConfigID, err)
	}
	_ = ctx // unused — minio-go's New() takes context internally
	return &s3publisher.Config{
		Bucket:       tc.Bucket,
		Endpoint:     tc.Endpoint, // public endpoint (the child writes from outside the VPC)
		Region:       tc.Region.String,
		UseSSL:       tc.UseSSL,
		AccessKey:    tc.AccessKey.String,
		SecretKey:    tc.SecretKey.String,
		BucketPrefix: prefix,
	}, nil
}

// renderIntrospectJSON returns the same JSON the local
// /api/v1/cp/introspect handler emits, marshaled to bytes. Used by
// the federation S3 publisher.
func (s *Server) renderIntrospectJSON(ctx context.Context) ([]byte, error) {
	_ = ctx
	resp := api.BuildIntrospectResponse(api.CPIntrospectDepsValue{
		Store:           s.store,
		Version:         Version(),
		DaemonVersionFn: s.daemonBinaryVersion,
		Features: api.AboutFeatures{
			OIDC:          s.oidc != nil,
			MgmtPlane:     s.cfg.MgmtListen != "",
			Tunnel:        s.tunReg != nil,
			WebhookIngest: s.cfg.Listen != "",
			Deploy:        s.cfg.DaemonBinaryPath != "" || s.cfg.DaemonBinariesDir != "",
		},
	})
	return json.Marshal(resp)
}

// renderFederationFindingsJSON returns the JSON the
// /api/v1/federation/findings endpoint would emit at default-filter
// (open status, latest 1000). The parent's aggregator caches this
// for s3 peers and serves federated /api/findings requests off it.
//
// Phase A.2 publishes the unfiltered list and the parent applies
// query-param filters client-side at the boundary (best-effort —
// see s3AssetForPath in the aggregator). Phase B+ may publish
// per-filter snapshots if operator UX demands it.
func (s *Server) renderFederationFindingsJSON(ctx context.Context) ([]byte, error) {
	_ = ctx
	return api.RenderFederationFindings(s.store, 1000)
}

// renderFederationDaimonsJSON / renderFederationNodesJSON /
// renderFederationOrchestrationsJSON — Phase A.3 siblings to the
// findings renderer. Same mechanic: produce the body the matching
// federation endpoint would emit at default filters.
func (s *Server) renderFederationDaimonsJSON(ctx context.Context) ([]byte, error) {
	_ = ctx
	return api.RenderFederationDaimons(s.store, 1000)
}

func (s *Server) renderFederationNodesJSON(ctx context.Context) ([]byte, error) {
	_ = ctx
	return api.RenderFederationNodes(s.store, 1000)
}

// renderFederationInvestigationsJSON — Phase 22.6 sibling to the
// other federation publisher renderers. Returns the same shape
// /api/v1/federation/investigations emits at default filters.
func (s *Server) renderFederationInvestigationsJSON(ctx context.Context) ([]byte, error) {
	_ = ctx
	return api.RenderFederationInvestigations(s.store, 500)
}

func (s *Server) renderFederationOrchestrationsJSON(ctx context.Context) ([]byte, error) {
	_ = ctx
	return api.RenderFederationOrchestrations(s.store)
}

// hooks so the rest of the CP doesn't need to know which transport
// the data arrived through.
func (s *Server) startS3Scanner(ctx context.Context, c db.TransportConfig) {
	// ScannerEndpoint resolves to endpoint_internal when set, else
	// the public endpoint — operators in split-horizon VPCs save
	// egress here, while everyone else gets the same single-endpoint
	// behavior the scanner had pre-Phase-9.x.
	cli, err := s3transport.NewClient(ctx, s3transport.ClientConfig{
		Endpoint:  c.ScannerEndpoint(),
		Region:    c.Region.String,
		Bucket:    c.Bucket,
		AccessKey: c.AccessKey.String,
		SecretKey: c.SecretKey.String,
		UseSSL:    c.UseSSL,
	})
	if err != nil {
		log.Printf("s3 scanner cfg=%d: connect: %v", c.ID, err)
		return
	}
	scanner := s3scanner.New(s.store, cli, c.CPID.String, c.ID, c.ScannerIntervalMs)
	scanner.IssueClientCert = func(commonName string) (cert, key, ca []byte, err error) {
		return s.IssueClientCert(commonName)
	}
	publish := func(e agent.Event, raw []byte) {
		// Reuse the webhook ingest path — publish to the events
		// queue and let the pipeline handle batching, persistence,
		// and SSE fan-out. Same shape webhooks land in.
		rec := ports.EventRecord{
			Ts:       e.Ts,
			Type:     string(e.Type),
			Agent:    e.Agent,
			Host:     e.Host,
			Severity: e.Severity,
			Title:    e.Title,
			RawJSON:  string(raw),
		}
		payload, err := json.Marshal(rec)
		if err != nil {
			return
		}
		_ = s.queue.Publish(ctx, eventpipeline.TopicEventsRaw, payload)
	}
	scanner.OnEvent = func(nodeID int64, e agent.Event) {
		raw, _ := json.Marshal(e)
		publish(e, raw)
	}
	scanner.OnFinding = func(nodeID int64, e agent.Event) {
		raw, _ := json.Marshal(e)
		publish(e, raw)
	}
	go scanner.Run(ctx)
	log.Printf("s3 scanner started: cfg=%d cp=%s bucket=%s every=%dms", c.ID, c.CPID.String, c.Bucket, c.ScannerIntervalMs)
}

func splitTargetTriple(t string) (string, string) {
	for i := len(t) - 1; i >= 0; i-- {
		if t[i] == '-' {
			return t[:i], t[i+1:]
		}
	}
	return "", ""
}

// mgmtRoutes wires the mTLS-protected agent management plane.
// All routes require a verified client cert; the agent's name is taken from
// the cert CN so URL/body fields cannot impersonate another agent.
func (s *Server) mgmtRoutes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(api.MTLSAccessLog)
	r.Post("/api/v1/agents/register", api.MgmtRegister(s.store))
	r.Post("/api/v1/agents/{name}/heartbeat", api.MgmtHeartbeat(s.store))
	r.Get("/api/v1/agents/{name}/config", api.MgmtConfig(s.store, s.cfg.DaimonFilesDir))
	r.Get("/api/v1/agents/{name}/definition", api.MgmtDefinition(s.cfg.DaimonFilesDir))
	r.Get("/api/v1/agents/{name}/known-issues", api.MgmtKnownIssues(s.store))
	r.Get("/api/v1/agents/{name}/findings/search", api.MgmtFindingsLookup(s.store))
	// Phase 22.2 — per-agent lessons (KV) the daemon prepends to its
	// system prompt each tick. Mounted on the mgmt plane (mTLS-gated)
	// alongside known-issues so the daemon can fetch via the same
	// authenticated channel it already uses.
	r.Get("/api/v1/agents/{name}/lessons", api.ListAgentLessonsHandler(s.store))

	// Pull-mode jobs queue (Phase D). The jobs runtime on each node
	// polls /jobs, claims work, streams output via /output, and
	// reports terminal state via /exit. Same mTLS gate as everything
	// else here — cert CN identifies the node.
	r.Get("/api/v1/agents/jobs", api.MgmtJobsPoll(s.store))
	r.Post("/api/v1/agents/jobs/{id}/output", api.MgmtJobOutput(s.store))
	r.Post("/api/v1/agents/jobs/{id}/exit", api.MgmtJobExit(s.store))

	// Reverse mTLS tunnel from `okesu node` clients (Phase 6).
	tunSrv := tunnel.NewServer(s.tunReg)
	r.Mount("/api/tunnel/connect", tunSrv.Handler())
	return r
}

// Run starts the HTTPS server(s) and blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	certFile, keyFile, err := EnsureTLSCert(s.cfg)
	if err != nil {
		return fmt.Errorf("tls cert: %w", err)
	}

	s.http.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}

	go s.sessionGC(ctx)
	go s.notify.Run(ctx)
	s.fedPoller.Start(ctx)
	// Phase A — S3 dead-drop federation: parent-side reader for
	// any federation_peers row with transport='s3_dead_drop'. No-op
	// when no S3 peers are registered, so it's safe to start
	// unconditionally; HTTPS-only operators pay nothing.
	//
	// The AssetCache holds the bucket-fetched findings/etc the
	// aggregator reads from for s3 peers. We construct it once and
	// hand it to both the reader (which writes into it) and the
	// aggregator (which reads from it via the S3Source interface).
	s3AssetCache := s3reader.NewAssetCache()
	s.fedAgg.SetS3Source(s3AssetCache)
	go s3reader.New(s.store, 0, s3AssetCache).Run(ctx)

	// Phase A — S3 dead-drop federation: child-side publisher.
	// Enabled when FederationS3PublishPrefix is set AND we have
	// either inline bucket coords (Phase A.1 enrollment bundle path)
	// or a transport_config_id pointing at an existing row. The
	// helper below picks the right path; the enable check just gates
	// the goroutine spawn.
	if s.cfg.FederationS3PublishPrefix != "" &&
		(s.cfg.FederationS3PublishConfigID > 0 || s.cfg.FederationS3PublishBucket != "") {
		s.startFederationS3Publisher(ctx)
		s.startFederationS3Dispatcher(ctx)
	}

	// Phase 22: SIGHUP triggers a re-scan of every IOC catalog
	// directory. Same code path as the boot-time load — operators
	// edit a YAML, kill -HUP <pid>, and the new entries are upserted
	// without a full restart. Errors per dir are logged; the handler
	// never exits so subsequent SIGHUPs after a bad reload still work.
	go s.iocCatalogReloader(ctx)
	// Phase D: cron-driven orchestration runs. The scheduler ticks
	// every minute and fires due orchestrations through the same
	// engine path as manual + finding triggers.
	s.orchestra.StartCronScheduler(ctx)

	// Phase 9: launch one S3 scanner per configured transport
	// config. Each scanner runs independently, sharing nothing
	// except the (read) DB store. Failures in one scanner don't
	// affect the others.
	if cfgs, err := s.store.ListTransportConfigs(); err != nil {
		log.Printf("s3 scanners: list transport_configs: %v", err)
	} else {
		for _, c := range cfgs {
			if c.Kind != "s3" || !c.AccessKey.Valid || !c.SecretKey.Valid {
				continue
			}
			s.startS3Scanner(ctx, c)
		}
	}

	// Reconcile any runs left in 'running' state from a previous CP process —
	// the child on the node may have finished while we were down, or be
	// orphaned. Either way, the row should not stay 'running' forever.
	if n, err := s.store.MarkInflightCancelled(); err != nil {
		log.Printf("runs: reconcile in-flight: %v", err)
	} else if n > 0 {
		log.Printf("runs: reconciled %d in-flight run(s) → cancelled", n)
	}

	// Retention: prune events older than EventTTLDays every 6 hours. Findings
	// are kept independently — operators want to keep the curated finding
	// table for trend analysis even after the raw event stream is rotated.
	if s.cfg.EventTTLDays > 0 {
		go s.retentionLoop(ctx)
	}

	errCh := make(chan error, 2)
	go func() {
		log.Printf("okesu-cp UI/webhook listening on https://%s (db=%s)", s.cfg.Listen, s.cfg.DBPath)
		err := s.http.ListenAndServeTLS(certFile, keyFile)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("ui server: %w", err)
			return
		}
		errCh <- nil
	}()

	if s.mgmtHTTP != nil {
		mgmtCert, mgmtKey, err := s.ensureMgmtCert()
		if err != nil {
			return fmt.Errorf("mgmt cert: %w", err)
		}
		caPool := x509.NewCertPool()
		caPool.AppendCertsFromPEM(s.ca.CertPEM)
		s.mgmtHTTP.TLSConfig = &tls.Config{
			MinVersion:   tls.VersionTLS13,
			ClientAuth:   tls.RequireAndVerifyClientCert,
			ClientCAs:    caPool,
			Certificates: []tls.Certificate{{}}, // populated below by ListenAndServeTLS
		}
		// Stdlib's ListenAndServeTLS reads cert/key from disk, but we want the
		// in-memory CA-signed pair. Build the cert here and clear Certificates
		// is filled by stdlib when files are passed; instead use the tls cert directly.
		s.mgmtHTTP.TLSConfig.Certificates = nil
		mgmtTLSCert, err := tls.X509KeyPair(mgmtCert, mgmtKey)
		if err != nil {
			return fmt.Errorf("parse mgmt cert: %w", err)
		}
		s.mgmtHTTP.TLSConfig.Certificates = []tls.Certificate{mgmtTLSCert}

		go func() {
			log.Printf("okesu-cp mgmt plane listening on https://%s (mTLS, ca=%s)", s.cfg.MgmtListen, s.ca.CertPath)
			// Empty cert/key paths — TLSConfig.Certificates is used instead.
			err := s.mgmtHTTP.ListenAndServeTLS("", "")
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("mgmt server: %w", err)
				return
			}
			errCh <- nil
		}()
	}

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.http.Shutdown(shutdownCtx)
		if s.mgmtHTTP != nil {
			_ = s.mgmtHTTP.Shutdown(shutdownCtx)
		}
		return s.store.Close()
	case err := <-errCh:
		_ = s.store.Close()
		return err
	}
}

// ensureMgmtCert returns the cert/key bytes used by the mgmt-plane TLS server.
// Uses cfg paths if provided, else generates a CA-signed cert next to the DB.
func (s *Server) ensureMgmtCert() (certPEM, keyPEM []byte, err error) {
	if s.cfg.MgmtCertFile != "" && s.cfg.MgmtKeyFile != "" {
		c, err1 := os.ReadFile(s.cfg.MgmtCertFile)
		k, err2 := os.ReadFile(s.cfg.MgmtKeyFile)
		if err1 == nil && err2 == nil {
			return c, k, nil
		}
	}
	dir := filepath.Dir(s.cfg.DBPath)
	certPath := filepath.Join(dir, "mgmt-server.crt")
	keyPath := filepath.Join(dir, "mgmt-server.key")
	if c, err1 := os.ReadFile(certPath); err1 == nil {
		if k, err2 := os.ReadFile(keyPath); err2 == nil {
			return c, k, nil
		}
	}
	// Include the configured public host (e.g. host.lima.internal) so daemons
	// dialing the CP via that hostname can verify the cert. Operators can add
	// more via OKESU_CP_MGMT_CERT_HOSTS (comma-separated).
	hosts := []string{"localhost", "okesu-cp", "okesu-cp.local"}
	for _, extra := range []string{
		extractHost(s.cfg.MgmtPublicURL),
		extractHost(s.cfg.WebhookPublicURL),
	} {
		if extra != "" && !contains(hosts, extra) {
			hosts = append(hosts, extra)
		}
	}
	if more := os.Getenv("OKESU_CP_MGMT_CERT_HOSTS"); more != "" {
		for _, h := range strings.Split(more, ",") {
			h = strings.TrimSpace(h)
			if h != "" && !contains(hosts, h) {
				hosts = append(hosts, h)
			}
		}
	}
	c, k, err := s.ca.IssueServerCert(hosts)
	if err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(certPath, c, 0644); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(keyPath, k, 0600); err != nil {
		return nil, nil, err
	}
	return c, k, nil
}

// CA exposes the in-memory CA so the issue-cert subcommand can sign
// client certs.
func (s *Server) CA() *CA { return s.ca }

// iocCatalogReloader listens for SIGHUP and re-runs the catalog
// loader against every configured directory. Lives until ctx is
// cancelled (i.e., until CP shutdown). Stays in its own goroutine so
// a slow reload (large catalog) doesn't block the rest of Run().
//
// Note: this is currently the only SIGHUP consumer in the CP. If a
// future reload (config rotate, log file reopen, etc.) wants to also
// listen for SIGHUP, fold those in here so signal.Notify only registers
// one channel — multiple Notify(SIGHUP) calls will fan out the same
// signal to every channel, but a single multiplexer keeps the logging
// coherent.
func (s *Server) iocCatalogReloader(ctx context.Context) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP)
	defer signal.Stop(sigCh)
	for {
		select {
		case <-ctx.Done():
			return
		case <-sigCh:
			log.Print("ioc catalog: SIGHUP — reloading")
			loadIOCCatalogs(s.cfg.IOCCatalogDirs, s.store)
		}
	}
}

// loadIOCCatalogs runs catalog.LoadAndUpsert against every configured
// directory, logging the loaded count or the error per dir. Errors do
// not block boot — if the operator has a typo'd YAML or a missing
// directory, the rest of the catalog still seeds and the CP comes up.
// Used both at boot from New() and from the SIGHUP handler in Run().
func loadIOCCatalogs(dirs []string, store *db.Store) {
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		n, err := catalog.LoadAndUpsert(dir, store)
		if err != nil {
			log.Printf("ioc catalog: %s: %v (continuing)", dir, err)
			continue
		}
		log.Printf("ioc catalog: loaded %d entries from %s", n, dir)
	}
}

// importDaemonBinaries scans dir for files named "okesu-<os>-<arch>" and
// upserts a row in the daemon_binaries table for each. Existing rows with
// the same name get refreshed (sha256 + size + path). Files that already
// have an up-to-date row are skipped.
func importDaemonBinaries(store *db.Store, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "okesu-") {
			continue
		}
		parts := strings.Split(name, "-")
		if len(parts) < 3 {
			continue
		}
		osName := parts[1]
		arch := strings.Join(parts[2:], "-")

		full := filepath.Join(dir, name)
		fi, err := os.Stat(full)
		if err != nil {
			continue
		}
		// If a row already exists with the same size, assume it's current.
		// (sha-checking every binary on every boot is wasteful; admins who
		// hand-replace files can still re-upload through the UI to refresh.)
		if existing, err := store.DaemonBinaryByName(name); err == nil &&
			existing.SizeBytes == fi.Size() && existing.Path == full {
			continue
		}
		// Compute sha256 lazily.
		sum, err := sha256File(full)
		if err != nil {
			log.Printf("import binary %s: hash failed: %v", name, err)
			continue
		}
		if err := store.UpsertDaemonBinary(&db.DaemonBinary{
			Name:      name,
			OS:        osName,
			Arch:      arch,
			Path:      full,
			SHA256:    sum,
			SizeBytes: fi.Size(),
		}, ""); err != nil {
			log.Printf("import binary %s: upsert failed: %v", name, err)
			continue
		}
		log.Printf("imported daemon binary %s (%s/%s, %d bytes)", name, osName, arch, fi.Size())
	}
	return nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractHost returns the hostname portion of a URL, or "" if parsing fails
// or the URL is empty. Used to populate TLS SANs from public-URL config.
func extractHost(u string) string {
	if u == "" {
		return ""
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// sessionGC periodically prunes expired sessions.
func (s *Server) sessionGC(ctx context.Context) {
	t := time.NewTicker(15 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = s.store.PruneSessions()
		}
	}
}

// retentionLoop trims the events table on a schedule. Runs an immediate prune
// on start so a freshly-configured TTL takes effect without waiting 6h.
func (s *Server) retentionLoop(ctx context.Context) {
	prune := func() {
		cutoff := time.Now().Add(-time.Duration(s.cfg.EventTTLDays) * 24 * time.Hour).UnixMilli()
		n, err := s.store.PruneEventsOlderThan(cutoff)
		if err != nil {
			log.Printf("retention: prune events: %v", err)
			return
		}
		if n > 0 {
			log.Printf("retention: pruned %d events older than %d days", n, s.cfg.EventTTLDays)
		}
	}
	prune()
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			prune()
		}
	}
}
