// routing-preflight checks deployed routing prerequisites with SELECT and read-only RPCs.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/mattn/go-sqlite3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/pkg/jsonx"
)

type check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}
type report struct {
	Stage      string           `json:"stage"`
	ObservedAt string           `json:"observed_at"`
	Checks     []check          `json:"checks"`
	Counts     map[string]int64 `json:"counts"`
	Warnings   []string         `json:"warnings,omitempty"`
}

func (r *report) add(name string, ok bool, detail string) {
	r.Checks = append(r.Checks, check{name, ok, detail})
}

type deployment struct {
	Services map[string]struct {
		Environment map[string]string `json:"environment"`
	} `json:"services"`
}

func configChecks(r *report, d deployment) {
	required := map[string][]string{"channel-service": {"CHANNEL_ROUTING_GROUP_DUAL_WRITE"}}
	if r.Stage >= "c" {
		required["identity-service"] = []string{"IDENTITY_ROUTING_V2"}
		required["billing-service"] = []string{"BILLING_REQUEST_SNAPSHOT_V2"}
		required["relay-gateway"] = []string{"RELAY_ROUTING_CONTEXT_V2"}
		for _, service := range []string{"identity-service", "billing-service", "relay-gateway", "admin-api"} {
			r.add(service+".channel_endpoint", strings.TrimSpace(d.Services[service].Environment["CHANNEL_GRPC_ENDPOINT"]) != "", "CHANNEL_GRPC_ENDPOINT must address the routing authority from this service")
		}
	}
	if r.Stage >= "d" {
		required["admin-api"] = []string{"ADMIN_ROUTING_FIXED_KEYS"}
	}
	if r.Stage >= "e" {
		for _, service := range []string{"admin-api", "billing-service", "relay-gateway"} {
			required[service] = append(required[service], "SUBSCRIPTION_ENTITLEMENTS_V2")
		}
	}
	if r.Stage >= "f" {
		required["admin-api"] = append(required["admin-api"], "ADMIN_ROUTING_ORDERED_KEYS")
		required["relay-gateway"] = append(required["relay-gateway"], "RELAY_ROUTING_ORDERED")
	}
	for _, service := range []string{"channel-service", "identity-service", "billing-service", "relay-gateway", "admin-api"} {
		for _, key := range required[service] {
			value := d.Services[service].Environment[key]
			// channel's existing dual-write gate is case-sensitive.
			on := strings.EqualFold(strings.TrimSpace(value), "true")
			if key == "CHANNEL_ROUTING_GROUP_DUAL_WRITE" {
				on = value == "true"
			}
			r.add(service+"."+key, on, "required for target stage; configure and recreate this service after prerequisites pass")
		}
	}
	flags := []bool{}
	for _, s := range []string{"admin-api", "billing-service", "relay-gateway"} {
		flags = append(flags, strings.EqualFold(strings.TrimSpace(d.Services[s].Environment["SUBSCRIPTION_ENTITLEMENTS_V2"]), "true"))
	}
	r.add("entitlements.consistent", flags[0] == flags[1] && flags[1] == flags[2], "admin, billing and relay must agree")
}

// SQLite accepts only an existing path. No caller pragma/DSN can turn this probe into a writer.
func sqliteDSN(raw string) (string, error) {
	path := strings.TrimPrefix(raw, "file:")
	if path == "" || path == ":memory:" || strings.ContainsAny(path, "?#") {
		return "", fmt.Errorf("use an existing SQLite path without options")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("SQLite file missing")
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	return u.String(), nil
}

var migrations = map[string]map[string][]string{
	"channel":  {"b": {"092_create_routing_groups"}, "d": {"095_create_routing_change_outbox"}, "f": {"100_channel_routing_relation_overrides"}},
	"identity": {"c": {"094_add_identity_routing_facts", "095_create_routing_change_outbox"}, "f": {"098_identity_ordered_token_groups"}},
	"billing":  {"c": {"093_add_request_snapshots"}, "d": {"095_create_routing_change_outbox"}, "e": {"096_add_subscription_contracts", "097_create_routing_billing_policies"}, "f": {"099_billing_user_routing_price_overrides"}},
}

func databaseChecks(ctx context.Context, r *report, db *sql.DB, owner, driver string) {
	level := sql.LevelRepeatableRead
	if driver == "sqlite3" {
		level = sql.LevelSerializable
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: level})
	if err != nil {
		r.add(owner+".database", false, "cannot open read-only transaction; verify access and driver")
		return
	}
	defer tx.Rollback()
	// Runner.Status initializes schema_migrations. Query existing metadata directly instead.
	rows, err := tx.QueryContext(ctx, "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		r.add(owner+".migrations", false, "existing schema_migrations must be readable; no metadata was created")
		return
	}
	applied := map[string]bool{}
	for rows.Next() {
		var version string
		if err = rows.Scan(&version); err != nil {
			break
		}
		applied[version] = true
	}
	readErr := rows.Err()
	rows.Close()
	if err != nil || readErr != nil {
		r.add(owner+".migrations", false, "cannot read migration metadata")
		return
	}
	ready := true
	for _, phase := range []string{"b", "c", "d", "e", "f"} {
		if phase > r.Stage {
			continue
		}
		for _, version := range migrations[owner][phase] {
			r.add(owner+"."+version, applied[version], "apply missing migration through the owning schema's migration workflow")
			ready = ready && applied[version]
		}
	}
	if !ready {
		return
	}
	count := func(name, query string) (int64, bool) {
		var n int64
		err := tx.QueryRowContext(ctx, query).Scan(&n)
		r.add(owner+"."+name+".readable", err == nil, "required schema/columns must exist and be readable")
		if err == nil {
			r.Counts[owner+"."+name] = n
		}
		return n, err == nil
	}
	switch owner {
	case "channel":
		n, ok := count("backfills", "SELECT COUNT(*) FROM routing_group_backfills")
		r.add("channel.backfill", ok && n > 0, "Phase B group audit/backfill must have a completion record")
		count("groups", "SELECT COUNT(*) FROM routing_groups")
	case "identity":
		if r.Stage < "c" {
			return
		}
		n, ok := count("unmapped_users", "SELECT COUNT(*) FROM users WHERE default_routing_group_id IS NULL OR default_routing_group_id = 0 OR routing_access_revision < 1")
		r.add("identity.backfill", ok && n == 0, "all users need routing facts; backfill is a separate write operation")
		for _, mode := range []string{"fixed", "ordered"} {
			count(mode+"_keys", "SELECT COUNT(*) FROM tokens WHERE routing_mode = '"+mode+"'")
		}
	case "billing":
		if r.Stage < "c" {
			return
		}
		// A frozen reservation prevents rolling back to binaries that ignore v2 evidence.
		count("active_v2_reservations", "SELECT COUNT(*) FROM billing_reservations WHERE status = 'reserved' AND request_snapshot IS NOT NULL AND request_snapshot <> ''")
		if r.Stage >= "e" {
			count("contracts", "SELECT COUNT(*) FROM user_subscriptions WHERE contract_snapshot IS NOT NULL AND contract_snapshot <> ''")
		}
	}
}

func rpcChecks(ctx context.Context, r *report, userID int64) {
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+os.Getenv("SERVICE_TOKEN"))
	for _, owner := range []string{"channel", "identity", "billing"} {
		if owner != "channel" && r.Stage < "c" {
			continue
		}
		endpoint := os.Getenv(strings.ToUpper(owner) + "_GRPC_ENDPOINT")
		if endpoint == "" {
			r.add(owner+".rpc", false, "set the service gRPC endpoint")
			continue
		}
		conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			r.add(owner+".rpc", false, "invalid service endpoint")
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		switch owner {
		case "channel":
			_, err = channelv1.NewChannelServiceClient(conn).ListRoutingGroups(probeCtx, &channelv1.ListRoutingGroupsRequest{PageSize: 1})
		case "identity":
			var reply *identityv1.GetUserRoutingFactsReply
			reply, err = identityv1.NewIdentityServiceClient(conn).GetUserRoutingFacts(probeCtx, &identityv1.GetUserRoutingFactsRequest{UserId: userID})
			if err == nil {
				r.add("identity.facts", reply.GetFacts().GetDefaultGroupId() > 0, "probe user must have mapped routing facts")
			}
		case "billing":
			var reply *billingv1.GetRoutingCapabilitiesResponse
			reply, err = billingv1.NewBillingServiceClient(conn).GetRoutingCapabilities(probeCtx, &billingv1.GetRoutingCapabilitiesRequest{})
			if err == nil {
				r.add("billing.snapshot_v2", reply.RequestSnapshotVersion == 2, "all billing instances and consumers must understand v2 snapshots")
				if r.Stage >= "d" {
					r.add("billing.fixed", reply.FixedRouting, "fixed routing capability required")
				}
				if r.Stage >= "e" {
					r.add("billing.contracts", reply.SubscriptionContracts, "subscription contract capability required")
				}
				if r.Stage >= "f" {
					r.add("billing.user_price", reply.UserPriceOverrides, "user price override capability required")
				}
			}
		}
		r.add(owner+".rpc", err == nil, "read-only routing RPC must succeed; check endpoint, service authentication and deployed version")
		cancel()
		conn.Close()
	}
}

func run(args []string, input io.Reader, output io.Writer) int {
	f := flag.NewFlagSet("routing-preflight", flag.ContinueOnError)
	f.SetOutput(output)
	stage := f.String("stage", "c", "target stage: b, c, d, e or f")
	driver := f.String("driver", "mysql", "mysql, postgres, sqlite3 (SQLite DSNs must be existing file paths)")
	userID := f.Int64("user-id", 1, "existing user for the read-only identity facts probe")
	if f.Parse(args) != nil || f.NArg() != 0 {
		return 2
	}
	if !strings.Contains("bcdef", *stage) || len(*stage) != 1 || *userID <= 0 || (*driver != "mysql" && *driver != "postgres" && *driver != "sqlite3") {
		fmt.Fprintln(output, "invalid stage, driver or user ID")
		return 2
	}
	r := &report{Stage: *stage, ObservedAt: time.Now().UTC().Format(time.RFC3339), Counts: map[string]int64{}}
	var config deployment
	if jsonx.NewDecoder(io.LimitReader(input, 8<<20)).Decode(&config) != nil || len(config.Services) == 0 {
		fmt.Fprintln(output, "pipe rendered Compose JSON (or equivalent services/environment JSON) on stdin")
		return 2
	}
	configChecks(r, config)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, owner := range []string{"channel", "identity", "billing"} {
		if owner != "channel" && *stage < "c" {
			continue
		}
		dsn := os.Getenv("ROUTING_" + strings.ToUpper(owner) + "_DSN")
		sqlDriver := *driver
		if sqlDriver == "postgres" {
			sqlDriver = "pgx"
		}
		var err error
		if *driver == "sqlite3" {
			dsn, err = sqliteDSN(dsn)
		}
		if err != nil || dsn == "" {
			r.add(owner+".database", false, "set ROUTING_"+strings.ToUpper(owner)+"_DSN to the existing owner database")
			continue
		}
		db, err := sql.Open(sqlDriver, dsn)
		if err != nil {
			r.add(owner+".database", false, "cannot open database")
			continue
		}
		db.SetMaxOpenConns(1)
		databaseChecks(ctx, r, db, owner, *driver)
		db.Close()
	}
	rpcChecks(ctx, r, *userID)
	if r.Counts["identity.fixed_keys"]+r.Counts["identity.ordered_keys"]+r.Counts["billing.contracts"] > 0 {
		r.Warnings = append(r.Warnings, "existing keys/contracts require v2-compatible readers; disabling creation gates does not remove these objects")
	}
	if r.Counts["billing.active_v2_reservations"] > 0 {
		r.Warnings = append(r.Warnings, "drain active v2 reservations before rollback; retain compatible settlement workers")
	}
	if jsonx.NewEncoder(output).Encode(r) != nil {
		return 2
	}
	for _, c := range r.Checks {
		if !c.OK {
			return 1
		}
	}
	return 0
}
func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout)) }
