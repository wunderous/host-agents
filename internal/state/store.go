package state

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db        *sql.DB
	closeOnce sync.Once
	closeErr  error
}

// PlanRecord is the durable envelope for a host-plan.v1 run. PlanJSON,
// RecipeJSON, and StateJSON contain only caller-approved, redacted JSON;
// secrets must be represented by references before a plan reaches this
// boundary.
type PlanRecord struct {
	RunID           string
	PlanID          string
	Generation      int
	IdempotencyKey  string
	DocumentHash    string
	CatalogRevision string
	Status          string
	PlanJSON        string
	RecipeJSON      string
	StateJSON       string
	CreatedAt       string
	UpdatedAt       string
	ErrorMessage    string
}

// ActiveCapabilityRecord is the host-local, product-neutral active selection.
// Runtime recipes and tunnel recipes use the same capability boundary; the
// provider string is descriptive metadata, never an executor selector.
type ActiveCapabilityRecord struct {
	Capability      string
	ServingContract string
	Provider        string
	// Runtime is retained for source compatibility with the initial runtime
	// recipe API. New records should populate Provider.
	Runtime           string
	RecipeID          string
	RecipeVersion     string
	RecipeHash        string
	RunID             string
	InputBindingsJSON string
	ObservationJSON   string
	ActivatedAt       string
}

// ProviderGenerationRecord is the durable projection of a provider module
// generation. The executable adapter is intentionally not persisted; after a
// restart the caller must reconnect the declared endpoint before using it.
type ProviderGenerationRecord struct {
	GenerationID    string
	ProviderID      string
	ProviderVersion string
	ManifestHash    string
	Endpoint        string
	CatalogRevision string
	Status          string
	CreatedAt       string
	ActiveAt        string
}

// ActiveRuntimeRecord is a compatibility alias for callers of the initial v1
// runtime recipe API.
type ActiveRuntimeRecord = ActiveCapabilityRecord

func Open(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("state directory is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "state.db"))
	if err != nil {
		return nil, fmt.Errorf("open standalone state: %w", err)
	}
	store := &Store{db: db}
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000; PRAGMA journal_mode = WAL;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure standalone state: %w", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS operations (
        operation_id TEXT PRIMARY KEY,
        tool_name TEXT NOT NULL,
        status TEXT NOT NULL,
        description TEXT,
        created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL,
        result_json TEXT,
        error_message TEXT
    );
    CREATE TABLE IF NOT EXISTS plan_runs (
        run_id TEXT PRIMARY KEY,
        plan_id TEXT NOT NULL,
        generation INTEGER NOT NULL,
        idempotency_key TEXT NOT NULL,
        document_hash TEXT NOT NULL,
        catalog_revision TEXT NOT NULL,
        status TEXT NOT NULL,
        plan_json TEXT NOT NULL,
        recipe_json TEXT NOT NULL DEFAULT '',
        state_json TEXT NOT NULL,
        created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL,
        error_message TEXT,
        UNIQUE(plan_id, generation, idempotency_key)
    );
    CREATE TABLE IF NOT EXISTS active_runtimes (
        capability TEXT PRIMARY KEY,
        serving_contract TEXT NOT NULL,
        runtime TEXT NOT NULL,
        recipe_id TEXT NOT NULL,
        recipe_version TEXT NOT NULL,
        recipe_hash TEXT NOT NULL,
        run_id TEXT NOT NULL,
        input_bindings_json TEXT NOT NULL,
        observation_json TEXT NOT NULL,
        activated_at TEXT NOT NULL
    );
    CREATE TABLE IF NOT EXISTS provider_generations (
        generation_id TEXT PRIMARY KEY,
        provider_id TEXT NOT NULL,
        provider_version TEXT NOT NULL,
        manifest_hash TEXT NOT NULL,
        endpoint TEXT NOT NULL,
        catalog_revision TEXT NOT NULL,
        status TEXT NOT NULL,
        created_at TEXT NOT NULL,
        active_at TEXT
    );
    UPDATE operations SET status = 'unknown', updated_at = datetime('now') WHERE status = 'working';
    UPDATE plan_runs SET status = 'unknown', updated_at = datetime('now') WHERE status IN ('working', 'running');`); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize standalone state: %w", err)
	}
	if err := ensurePlanRecipeColumn(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate standalone plan state: %w", err)
	}
	if err := ensureActiveCapabilityTable(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate active capability state: %w", err)
	}
	return store, nil
}

func ensureActiveCapabilityTable(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS active_capabilities (
        capability TEXT PRIMARY KEY,
        serving_contract TEXT NOT NULL,
        provider TEXT NOT NULL,
        recipe_id TEXT NOT NULL,
        recipe_version TEXT NOT NULL,
        recipe_hash TEXT NOT NULL,
        run_id TEXT NOT NULL,
        input_bindings_json TEXT NOT NULL,
        observation_json TEXT NOT NULL,
        activated_at TEXT NOT NULL
    )`); err != nil {
		return err
	}
	// Older state directories used active_runtimes. Copying the rows is safe
	// and lets a restart upgrade state without losing the active selection.
	var exists int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='active_runtimes'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 1 {
		// Very old standalone state directories predate the durable recipe
		// projection and do not have every column used by active_runtimes.
		// Add the missing nullable-in-practice fields before selecting them;
		// otherwise startup fails before the agent can serve a migration or
		// health request.
		for _, column := range []struct {
			name string
			def  string
		}{
			{name: "recipe_id", def: "TEXT NOT NULL DEFAULT ''"},
			{name: "recipe_version", def: "TEXT NOT NULL DEFAULT ''"},
			{name: "recipe_hash", def: "TEXT NOT NULL DEFAULT ''"},
			{name: "run_id", def: "TEXT NOT NULL DEFAULT ''"},
			{name: "input_bindings_json", def: "TEXT NOT NULL DEFAULT '{}'"},
			{name: "observation_json", def: "TEXT NOT NULL DEFAULT '{}'"},
			{name: "activated_at", def: "TEXT NOT NULL DEFAULT ''"},
		} {
			if err := ensureTableColumn(db, "active_runtimes", column.name, column.def); err != nil {
				return err
			}
		}
		_, err := db.Exec(`INSERT OR IGNORE INTO active_capabilities(
            capability, serving_contract, provider, recipe_id, recipe_version,
            recipe_hash, run_id, input_bindings_json, observation_json, activated_at
        ) SELECT capability, serving_contract, runtime, recipe_id, recipe_version,
            recipe_hash, run_id, input_bindings_json, observation_json, activated_at
        FROM active_runtimes`)
		return err
	}
	return nil
}

func ensureTableColumn(db *sql.DB, table, column, definition string) error {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if name == column {
			return rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + definition)
	return err
}

func ensurePlanRecipeColumn(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(plan_runs)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if name == "recipe_json" {
			return rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(`ALTER TABLE plan_runs ADD COLUMN recipe_json TEXT NOT NULL DEFAULT ''`)
	return err
}

// CreatePlan inserts a new run. The boolean reports whether this call inserted
// the record instead of returning an existing run with the same identity.
func (s *Store) CreatePlan(record PlanRecord) (PlanRecord, bool, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if record.CreatedAt == "" {
		record.CreatedAt = now
	}
	if record.UpdatedAt == "" {
		record.UpdatedAt = now
	}
	_, err := s.db.Exec(`INSERT INTO plan_runs(
        run_id, plan_id, generation, idempotency_key, document_hash,
        catalog_revision, status, plan_json, recipe_json, state_json, created_at, updated_at,
        error_message
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
    ON CONFLICT(plan_id, generation, idempotency_key) DO NOTHING`,
		record.RunID, record.PlanID, record.Generation, record.IdempotencyKey,
		record.DocumentHash, record.CatalogRevision, record.Status,
		record.PlanJSON, record.RecipeJSON, record.StateJSON, record.CreatedAt, record.UpdatedAt)
	if err != nil {
		return PlanRecord{}, false, err
	}
	created := false
	if existing, found, getErr := s.FindPlan(record.PlanID, record.Generation, record.IdempotencyKey); getErr != nil {
		return PlanRecord{}, false, getErr
	} else if found {
		created = existing.RunID == record.RunID
		return existing, created, nil
	}
	return PlanRecord{}, false, fmt.Errorf("plan run was not persisted: %s", record.RunID)
}

func (s *Store) FindPlan(planID string, generation int, idempotencyKey string) (PlanRecord, bool, error) {
	row := s.db.QueryRow(`SELECT run_id, plan_id, generation, idempotency_key,
        document_hash, catalog_revision, status, plan_json, recipe_json, state_json,
        created_at, updated_at, COALESCE(error_message, '')
        FROM plan_runs WHERE plan_id = ? AND generation = ? AND idempotency_key = ?`,
		planID, generation, idempotencyKey)
	return scanPlan(row)
}

func (s *Store) GetPlan(runID string) (PlanRecord, bool, error) {
	row := s.db.QueryRow(`SELECT run_id, plan_id, generation, idempotency_key,
        document_hash, catalog_revision, status, plan_json, recipe_json, state_json,
        created_at, updated_at, COALESCE(error_message, '')
        FROM plan_runs WHERE run_id = ?`, runID)
	return scanPlan(row)
}

func (s *Store) UpdatePlan(runID, status, stateJSON, errorMessage string) error {
	_, err := s.db.Exec(`UPDATE plan_runs SET status = ?, state_json = ?,
        updated_at = ?, error_message = NULLIF(?, '') WHERE run_id = ?`,
		status, stateJSON, time.Now().UTC().Format(time.RFC3339Nano), errorMessage, runID)
	return err
}

// UpdatePlanCatalogRevision records the catalog against which a resumed plan
// was revalidated. A plan may be resumed after a compatible catalog change;
// the host-plan validator and runner still validate every node against the
// current catalog before taking action.
func (s *Store) UpdatePlanCatalogRevision(runID, catalogRevision string) error {
	_, err := s.db.Exec(`UPDATE plan_runs SET catalog_revision = ?, updated_at = ? WHERE run_id = ?`,
		catalogRevision, time.Now().UTC().Format(time.RFC3339Nano), runID)
	return err
}

func (s *Store) SaveProviderGeneration(record ProviderGenerationRecord) error {
	if record.GenerationID == "" || record.ProviderID == "" || record.ProviderVersion == "" || record.ManifestHash == "" || record.Endpoint == "" {
		return fmt.Errorf("provider generation identity, manifest hash, and endpoint are required")
	}
	if record.CreatedAt == "" {
		record.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.Exec(`INSERT INTO provider_generations(
        generation_id, provider_id, provider_version, manifest_hash, endpoint,
        catalog_revision, status, created_at, active_at
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''))
    ON CONFLICT(generation_id) DO UPDATE SET
        provider_id=excluded.provider_id,
        provider_version=excluded.provider_version,
        manifest_hash=excluded.manifest_hash,
        endpoint=excluded.endpoint,
        catalog_revision=excluded.catalog_revision,
        status=excluded.status,
        active_at=excluded.active_at`,
		record.GenerationID, record.ProviderID, record.ProviderVersion, record.ManifestHash,
		record.Endpoint, record.CatalogRevision, record.Status, record.CreatedAt, record.ActiveAt)
	return err
}

func (s *Store) ListProviderGenerations() ([]ProviderGenerationRecord, error) {
	rows, err := s.db.Query(`SELECT generation_id, provider_id, provider_version,
        manifest_hash, endpoint, catalog_revision, status, created_at,
        COALESCE(active_at, '') FROM provider_generations ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []ProviderGenerationRecord
	for rows.Next() {
		var record ProviderGenerationRecord
		if err := rows.Scan(&record.GenerationID, &record.ProviderID, &record.ProviderVersion,
			&record.ManifestHash, &record.Endpoint, &record.CatalogRevision, &record.Status,
			&record.CreatedAt, &record.ActiveAt); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

// CompletePlanWithActiveRuntime commits the successful plan and the active
// capability selection in one SQLite transaction. This prevents a validated
// runtime from becoming active while its durable recipe run is still marked
// failed or unknown.
func (s *Store) CompletePlanWithActiveRuntime(runID, stateJSON string, active ActiveRuntimeRecord) error {
	if active.Provider == "" {
		active.Provider = active.Runtime
	}
	return s.CompletePlanWithActiveCapability(runID, stateJSON, active)
}

func (s *Store) CompletePlanWithActiveCapability(runID, stateJSON string, active ActiveCapabilityRecord) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`UPDATE plan_runs SET status = 'completed', state_json = ?, updated_at = ?, error_message = NULL WHERE run_id = ?`, stateJSON, now, runID); err != nil {
		return err
	}
	if active.ActivatedAt == "" {
		active.ActivatedAt = now
	}
	if _, err := tx.Exec(`INSERT INTO active_capabilities(
        capability, serving_contract, provider, recipe_id, recipe_version,
        recipe_hash, run_id, input_bindings_json, observation_json, activated_at
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT(capability) DO UPDATE SET
        serving_contract=excluded.serving_contract,
		provider=excluded.provider,
        recipe_id=excluded.recipe_id,
        recipe_version=excluded.recipe_version,
        recipe_hash=excluded.recipe_hash,
        run_id=excluded.run_id,
        input_bindings_json=excluded.input_bindings_json,
        observation_json=excluded.observation_json,
        activated_at=excluded.activated_at`,
		active.Capability, active.ServingContract, firstNonEmpty(active.Provider, active.Runtime), active.RecipeID,
		active.RecipeVersion, active.RecipeHash, active.RunID, active.InputBindingsJSON,
		active.ObservationJSON, active.ActivatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) GetActiveRuntime(capability string) (ActiveRuntimeRecord, bool, error) {
	return s.GetActiveCapability(capability)
}

func (s *Store) GetActiveCapability(capability string) (ActiveCapabilityRecord, bool, error) {
	row := s.db.QueryRow(`SELECT capability, serving_contract, provider, recipe_id,
		recipe_version, recipe_hash, run_id, input_bindings_json,
		observation_json, activated_at FROM active_capabilities WHERE capability = ?`, capability)
	var record ActiveCapabilityRecord
	if err := row.Scan(&record.Capability, &record.ServingContract, &record.Runtime,
		&record.RecipeID, &record.RecipeVersion, &record.RecipeHash, &record.RunID,
		&record.InputBindingsJSON, &record.ObservationJSON, &record.ActivatedAt); err == sql.ErrNoRows {
		return ActiveCapabilityRecord{}, false, nil
	} else if err != nil {
		return ActiveCapabilityRecord{}, false, err
	}
	record.Provider = record.Runtime
	return record, true, nil
}

// RemoveActiveCapabilitiesForProvider clears active selections owned by a
// provider after its explicitly confirmed teardown has completed.
func (s *Store) RemoveActiveCapabilitiesForProvider(provider string) error {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return fmt.Errorf("provider is required")
	}
	_, err := s.db.Exec(`DELETE FROM active_capabilities WHERE provider = ?`, provider)
	return err
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func scanPlan(row rowScanner) (PlanRecord, bool, error) {
	var record PlanRecord
	if err := row.Scan(
		&record.RunID, &record.PlanID, &record.Generation, &record.IdempotencyKey,
		&record.DocumentHash, &record.CatalogRevision, &record.Status,
		&record.PlanJSON, &record.RecipeJSON, &record.StateJSON, &record.CreatedAt, &record.UpdatedAt,
		&record.ErrorMessage,
	); err == sql.ErrNoRows {
		return PlanRecord{}, false, nil
	} else if err != nil {
		return PlanRecord{}, false, err
	}
	return record, true, nil
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		if s.db != nil {
			s.closeErr = s.db.Close()
		}
	})
	return s.closeErr
}

func (s *Store) Create(operationID, toolName, description string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`INSERT OR REPLACE INTO operations(operation_id, tool_name, status, description, created_at, updated_at) VALUES (?, ?, 'working', ?, ?, ?)`, operationID, toolName, description, now, now)
	return err
}

func (s *Store) Complete(operationID string, result any) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE operations SET status = 'completed', updated_at = ?, result_json = ?, error_message = NULL WHERE operation_id = ?`, time.Now().UTC().Format(time.RFC3339Nano), string(encoded), operationID)
	return err
}

func (s *Store) Fail(operationID, message string) error {
	_, err := s.db.Exec(`UPDATE operations SET status = 'failed', updated_at = ?, error_message = ? WHERE operation_id = ?`, time.Now().UTC().Format(time.RFC3339Nano), message, operationID)
	return err
}

func (s *Store) Cancel(operationID string) error {
	_, err := s.db.Exec(`UPDATE operations SET status = 'cancelled', updated_at = ? WHERE operation_id = ? AND status IN ('working', 'unknown')`, time.Now().UTC().Format(time.RFC3339Nano), operationID)
	return err
}

func (s *Store) List(limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT operation_id, tool_name, status, description, created_at, updated_at, result_json, error_message FROM operations ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		item, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) Get(operationID string) (map[string]any, bool, error) {
	row := s.db.QueryRow(`SELECT operation_id, tool_name, status, description, created_at, updated_at, result_json, error_message FROM operations WHERE operation_id = ?`, operationID)
	item, err := scanOperation(row)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return item, true, nil
}

type rowScanner interface{ Scan(dest ...any) error }

func scanOperation(row rowScanner) (map[string]any, error) {
	var id, tool, status, description, created, updated string
	var result, message sql.NullString
	if err := row.Scan(&id, &tool, &status, &description, &created, &updated, &result, &message); err != nil {
		return nil, err
	}
	item := map[string]any{"operationId": id, "toolName": tool, "status": status, "description": description, "createdAt": created, "updatedAt": updated}
	if result.Valid && result.String != "" {
		var parsed any
		if json.Unmarshal([]byte(result.String), &parsed) == nil {
			item["result"] = parsed
		}
	}
	if message.Valid && message.String != "" {
		item["error"] = message.String
	}
	return item, nil
}
