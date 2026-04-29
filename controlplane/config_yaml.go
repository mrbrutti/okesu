package controlplane

import (
	"context"
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"

	"github.com/section9labs/okesu/controlplane/ports"
)

// LoadConfigFile reads a YAML config file and merges it into a Config
// struct. The YAML schema mirrors the field names with snake_case —
// see deploy/oci/cp.example.yaml for the canonical shape. Fields not
// in the YAML keep their zero / env-default value, and CLI flags
// override afterwards.
//
// Secret references take the form "${secret:NAME}" — they are NOT
// resolved by this loader. Resolution happens in resolveSecrets at
// boot, after the secrets-source adapter is constructed. The YAML
// just records "this field needs the value of secret NAME" without
// embedding the value itself.
//
// Why YAML instead of TOML / JSON / HCL: yaml.v3 is already a
// dependency (used for daimon frontmatter parsing); operators are
// already used to it from the daimon library. Stays consistent.
func LoadConfigFile(path string, into *Config) error {
	if path == "" {
		return nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config file: %w", err)
	}

	// Parse into a struct that mirrors Config but uses YAML-friendly
	// field tags. Two-step: parse to the YAML struct, then merge the
	// non-zero fields into the live Config.
	var y yamlConfig
	if err := yaml.Unmarshal(body, &y); err != nil {
		return fmt.Errorf("parse config file: %w", err)
	}
	y.mergeInto(into)
	return nil
}

// yamlConfig is the YAML-shape mirror of Config. Pointer types
// distinguish "field absent" (nil → don't override) from "field
// explicitly set to zero" (non-nil pointing at a zero — DOES
// override). Without this, a YAML file omitting a field would
// silently overwrite an env var or flag value with the zero.
type yamlConfig struct {
	Listen        *string  `yaml:"listen"`
	MgmtListen    *string  `yaml:"mgmt_listen"`
	DBPath        *string  `yaml:"db"`
	WebhookSecret *string  `yaml:"webhook_secret"`
	AdminEmail    *string  `yaml:"admin_email"`
	AdminPassword *string  `yaml:"admin_password"`
	SessionKey    *string  `yaml:"session_key"`

	OIDC struct {
		Issuer       *string `yaml:"issuer"`
		ClientID     *string `yaml:"client_id"`
		ClientSecret *string `yaml:"client_secret"`
		RedirectURL  *string `yaml:"redirect_url"`
		GroupsClaim  *string `yaml:"groups_claim"`
		RoleMap      *string `yaml:"role_map"`
		Label        *string `yaml:"label"`
	} `yaml:"oidc"`

	DaemonBinaryPath  *string `yaml:"daemon_binary"`
	DaemonBinariesDir *string `yaml:"daemon_binaries_dir"`
	DaimonFilesDir    *string `yaml:"daimon_files_dir"`
	AgentFilesDirs    []string `yaml:"agent_files_dirs"`
	IOCCatalogDirs    []string `yaml:"ioc_catalog_dirs"`

	WebhookPublicURL *string `yaml:"webhook_public_url"`
	MgmtPublicURL    *string `yaml:"mgmt_public_url"`

	EventTTLDays *int `yaml:"event_ttl_days"`

	PubSubURL *string `yaml:"pubsub_url"`

	SecretsSource *string `yaml:"secrets_source"`

	BlobStoreURL  *string `yaml:"blob_url"`
	BlobAccessKey *string `yaml:"blob_access_key"`
	BlobSecretKey *string `yaml:"blob_secret_key"`
	BlobBucket    *string `yaml:"blob_bucket"`
	BlobRegion    *string `yaml:"blob_region"`

	EventsStore        *string  `yaml:"events_store"`
	ClickHouseAddrs    []string `yaml:"clickhouse_addrs"`
	ClickHouseDatabase *string  `yaml:"clickhouse_database"`
	ClickHouseUsername *string  `yaml:"clickhouse_username"`
	ClickHousePassword *string  `yaml:"clickhouse_password"`
	ClickHouseSecure   *bool    `yaml:"clickhouse_secure"`

	Queue             *string  `yaml:"queue"`
	KafkaBrokers      []string `yaml:"kafka_brokers"`
	KafkaSASLUsername *string  `yaml:"kafka_sasl_username"`
	KafkaSASLPassword *string  `yaml:"kafka_sasl_password"`
	KafkaUseTLS       *bool    `yaml:"kafka_use_tls"`

	// Policy is the per-class auto-approve toggle the orchestrator
	// consults at the step-approval gate. YAML shape:
	//
	//   policy:
	//     auto_approve:
	//       read: true
	//       enrich: true
	//
	// Absent = empty map = no class auto-approved (today's behaviour).
	// See controlplane/orchestrator/action_class.go for the class set.
	Policy struct {
		AutoApprove map[string]bool `yaml:"auto_approve"`
	} `yaml:"policy"`
}

// mergeInto applies non-nil fields from y onto cfg. Zero pointer-deref
// values are intentional overrides; nil pointers leave cfg alone.
func (y *yamlConfig) mergeInto(cfg *Config) {
	setStr := func(src *string, dst *string) {
		if src != nil {
			*dst = *src
		}
	}
	setStrSlice := func(src []string, dst *[]string) {
		if src != nil {
			*dst = src
		}
	}
	setInt := func(src *int, dst *int) {
		if src != nil {
			*dst = *src
		}
	}
	setBool := func(src *bool, dst *bool) {
		if src != nil {
			*dst = *src
		}
	}

	setStr(y.Listen, &cfg.Listen)
	setStr(y.MgmtListen, &cfg.MgmtListen)
	setStr(y.DBPath, &cfg.DBPath)
	setStr(y.WebhookSecret, &cfg.WebhookSecret)
	setStr(y.AdminEmail, &cfg.AdminEmail)
	setStr(y.AdminPassword, &cfg.AdminPassword)
	setStr(y.SessionKey, &cfg.SessionKey)

	setStr(y.OIDC.Issuer, &cfg.OIDCIssuer)
	setStr(y.OIDC.ClientID, &cfg.OIDCClientID)
	setStr(y.OIDC.ClientSecret, &cfg.OIDCClientSecret)
	setStr(y.OIDC.RedirectURL, &cfg.OIDCRedirectURL)
	setStr(y.OIDC.GroupsClaim, &cfg.OIDCGroupsClaim)
	setStr(y.OIDC.RoleMap, &cfg.OIDCRoleMap)
	setStr(y.OIDC.Label, &cfg.OIDCLabel)

	setStr(y.DaemonBinaryPath, &cfg.DaemonBinaryPath)
	setStr(y.DaemonBinariesDir, &cfg.DaemonBinariesDir)
	setStr(y.DaimonFilesDir, &cfg.DaimonFilesDir)
	setStrSlice(y.AgentFilesDirs, &cfg.AgentFilesDirs)
	setStrSlice(y.IOCCatalogDirs, &cfg.IOCCatalogDirs)

	setStr(y.WebhookPublicURL, &cfg.WebhookPublicURL)
	setStr(y.MgmtPublicURL, &cfg.MgmtPublicURL)

	setInt(y.EventTTLDays, &cfg.EventTTLDays)

	setStr(y.PubSubURL, &cfg.PubSubURL)

	setStr(y.SecretsSource, &cfg.SecretsSource)

	setStr(y.BlobStoreURL, &cfg.BlobStoreURL)
	setStr(y.BlobAccessKey, &cfg.BlobAccessKey)
	setStr(y.BlobSecretKey, &cfg.BlobSecretKey)
	setStr(y.BlobBucket, &cfg.BlobBucket)
	setStr(y.BlobRegion, &cfg.BlobRegion)

	setStr(y.EventsStore, &cfg.EventsStore)
	setStrSlice(y.ClickHouseAddrs, &cfg.ClickHouseAddrs)
	setStr(y.ClickHouseDatabase, &cfg.ClickHouseDatabase)
	setStr(y.ClickHouseUsername, &cfg.ClickHouseUsername)
	setStr(y.ClickHousePassword, &cfg.ClickHousePassword)
	setBool(y.ClickHouseSecure, &cfg.ClickHouseSecure)

	setStr(y.Queue, &cfg.Queue)
	setStrSlice(y.KafkaBrokers, &cfg.KafkaBrokers)
	setStr(y.KafkaSASLUsername, &cfg.KafkaSASLUsername)
	setStr(y.KafkaSASLPassword, &cfg.KafkaSASLPassword)
	setBool(y.KafkaUseTLS, &cfg.KafkaUseTLS)

	// Policy.AutoApprove: nil-vs-empty distinction matters so an
	// operator who wrote `policy: { auto_approve: {} }` to clear an
	// inherited default still ends up with an empty (non-nil) map.
	// yaml.v3 returns nil when the key is absent; we only override
	// the live config when the YAML supplied a value.
	if y.Policy.AutoApprove != nil {
		cfg.Policy.AutoApprove = y.Policy.AutoApprove
	}
}

// secretRefRE matches "${secret:NAME}" inside a config string. Names
// follow the canonical hierarchy convention (slash-separated; see
// controlplane/secrets.go for the constants).
var secretRefRE = regexp.MustCompile(`\$\{secret:([^}]+)\}`)

// resolveConfigSecretRefs walks the secret-bearing string fields and
// replaces "${secret:NAME}" references with values pulled from the
// adapter. Called BEFORE resolveSecrets so the order is:
//
//   1. Load YAML → cfg field has "${secret:cp/admin-password}"
//   2. resolveConfigSecretRefs → cfg field gets the value from vault
//   3. resolveSecrets → fills any STILL-empty secret fields by name
//
// Step 2 + 3 are complementary: step 2 supports "I named the secret
// here" (operator-driven naming, lets you reference the same secret
// from multiple fields); step 3 supports "I just want the standard
// secret for this purpose" (CP-driven canonical names).
func resolveConfigSecretRefs(ctx context.Context, cfg *Config, s ports.Secrets) error {
	expand := func(field *string) error {
		if !secretRefRE.MatchString(*field) {
			return nil
		}
		matches := secretRefRE.FindAllStringSubmatchIndex(*field, -1)
		// Walk in reverse so earlier indices stay valid as we substitute.
		out := *field
		for i := len(matches) - 1; i >= 0; i-- {
			m := matches[i]
			start, end := m[0], m[1]
			nameStart, nameEnd := m[2], m[3]
			name := out[nameStart:nameEnd]
			val, gerr := s.Get(ctx, name)
			if gerr != nil {
				return fmt.Errorf("resolve ${secret:%s}: %w", name, gerr)
			}
			out = out[:start] + string(val) + out[end:]
		}
		*field = out
		return nil
	}

	for _, f := range []*string{
		&cfg.WebhookSecret, &cfg.AdminPassword, &cfg.SessionKey,
		&cfg.OIDCClientSecret,
		&cfg.ClickHousePassword, &cfg.KafkaSASLPassword, &cfg.BlobSecretKey,
	} {
		if err := expand(f); err != nil {
			return err
		}
	}
	return nil
}
