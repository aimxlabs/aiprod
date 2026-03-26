package tables

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/garett/aiprod/internal/db"
)

var registryMigrations = []string{
	`CREATE TABLE IF NOT EXISTS table_registry (
		id          TEXT PRIMARY KEY,
		name        TEXT UNIQUE NOT NULL,
		description TEXT DEFAULT '',
		created_at  TEXT NOT NULL,
		modified_at TEXT NOT NULL,
		row_count   INTEGER DEFAULT 0
	);

	CREATE TABLE IF NOT EXISTS table_columns (
		table_id  TEXT NOT NULL REFERENCES table_registry(id),
		name      TEXT NOT NULL,
		type      TEXT NOT NULL,
		required  INTEGER DEFAULT 0,
		position  INTEGER NOT NULL,
		PRIMARY KEY (table_id, name)
	);`,
}

type Store struct {
	registryDB *sql.DB // core.db for registry
	tablesDB   *sql.DB // tables.db for user tables
}

type Table struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Columns     []Column `json:"columns"`
	CreatedAt   string   `json:"created_at"`
	ModifiedAt  string   `json:"modified_at"`
	RowCount    int      `json:"row_count"`
}

type Column struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
	Position int    `json:"position"`
}

func NewStore(coreDB *sql.DB, tablesDBPath string) (*Store, error) {
	if err := db.Migrate(coreDB, "tables", registryMigrations); err != nil {
		return nil, fmt.Errorf("migrating tables registry: %w", err)
	}
	tdb, err := db.Open(tablesDBPath)
	if err != nil {
		return nil, fmt.Errorf("opening tables db: %w", err)
	}
	return &Store{registryDB: coreDB, tablesDB: tdb}, nil
}

func (s *Store) Close() error {
	return s.tablesDB.Close()
}

func newID(prefix string) string {
	b := make([]byte, 12)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

var validTypes = map[string]string{
	"text":    "TEXT",
	"integer": "INTEGER",
	"real":    "REAL",
	"boolean": "INTEGER",
	"date":    "TEXT",
	"json":    "TEXT",
}

func (s *Store) Create(name, description string, columns []Column) (*Table, error) {
	id := newID("tbl_")
	now := time.Now().UTC().Format(time.RFC3339)
	sqlName := "ut_" + name

	// Build CREATE TABLE
	colDefs := []string{
		"_rowid INTEGER PRIMARY KEY AUTOINCREMENT",
		"_created_at TEXT DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))",
		"_modified_at TEXT DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))",
	}
	for _, c := range columns {
		sqlType, ok := validTypes[c.Type]
		if !ok {
			return nil, fmt.Errorf("invalid column type: %s (valid: text, integer, real, boolean, date, json)", c.Type)
		}
		def := fmt.Sprintf("%s %s", c.Name, sqlType)
		if c.Required {
			def += " NOT NULL"
		}
		colDefs = append(colDefs, def)
	}

	createSQL := fmt.Sprintf("CREATE TABLE %s (%s)", sqlName, strings.Join(colDefs, ", "))
	if _, err := s.tablesDB.Exec(createSQL); err != nil {
		return nil, fmt.Errorf("creating table: %w", err)
	}

	// Register in core db
	if _, err := s.registryDB.Exec(
		"INSERT INTO table_registry (id, name, description, created_at, modified_at) VALUES (?, ?, ?, ?, ?)",
		id, name, description, now, now,
	); err != nil {
		return nil, fmt.Errorf("registering table: %w", err)
	}

	for i, c := range columns {
		if _, err := s.registryDB.Exec(
			"INSERT INTO table_columns (table_id, name, type, required, position) VALUES (?, ?, ?, ?, ?)",
			id, c.Name, c.Type, c.Required, i,
		); err != nil {
			return nil, fmt.Errorf("registering column: %w", err)
		}
	}

	return &Table{
		ID: id, Name: name, Description: description,
		Columns: columns, CreatedAt: now, ModifiedAt: now,
	}, nil
}

func (s *Store) Get(nameOrID string) (*Table, error) {
	t := &Table{}
	err := s.registryDB.QueryRow(
		"SELECT id, name, description, created_at, modified_at, row_count FROM table_registry WHERE id = ? OR name = ?",
		nameOrID, nameOrID,
	).Scan(&t.ID, &t.Name, &t.Description, &t.CreatedAt, &t.ModifiedAt, &t.RowCount)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting table: %w", err)
	}

	rows, err := s.registryDB.Query(
		"SELECT name, type, required, position FROM table_columns WHERE table_id = ? ORDER BY position", t.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("getting columns: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c Column
		if err := rows.Scan(&c.Name, &c.Type, &c.Required, &c.Position); err != nil {
			return nil, err
		}
		t.Columns = append(t.Columns, c)
	}
	return t, nil
}

func (s *Store) List() ([]Table, error) {
	rows, err := s.registryDB.Query(
		"SELECT id, name, description, created_at, modified_at, row_count FROM table_registry ORDER BY name",
	)
	if err != nil {
		return nil, fmt.Errorf("listing tables: %w", err)
	}
	defer rows.Close()

	var tables []Table
	for rows.Next() {
		var t Table
		if err := rows.Scan(&t.ID, &t.Name, &t.Description, &t.CreatedAt, &t.ModifiedAt, &t.RowCount); err != nil {
			return nil, err
		}
		tables = append(tables, t)
	}
	return tables, nil
}

func (s *Store) InsertRow(tableName string, data map[string]interface{}) (int64, error) {
	sqlName := "ut_" + tableName
	cols := []string{}
	placeholders := []string{}
	vals := []interface{}{}

	for k, v := range data {
		cols = append(cols, k)
		placeholders = append(placeholders, "?")
		vals = append(vals, v)
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", sqlName, strings.Join(cols, ","), strings.Join(placeholders, ","))
	res, err := s.tablesDB.Exec(query, vals...)
	if err != nil {
		return 0, fmt.Errorf("inserting row: %w", err)
	}

	// Update row count
	s.registryDB.Exec("UPDATE table_registry SET row_count = row_count + 1, modified_at = ? WHERE name = ?",
		time.Now().UTC().Format(time.RFC3339), tableName)

	return res.LastInsertId()
}

func (s *Store) QueryRows(tableName, where, orderBy string, limit, offset int) ([]map[string]interface{}, error) {
	sqlName := "ut_" + tableName
	query := "SELECT * FROM " + sqlName
	var args []interface{}

	if where != "" {
		query += " WHERE " + where
	}
	if orderBy != "" {
		query += " ORDER BY " + orderBy
	}
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	} else {
		query += " LIMIT 100"
	}
	if offset > 0 {
		query += fmt.Sprintf(" OFFSET %d", offset)
	}

	return s.execQuery(query, args...)
}

func (s *Store) ExecSQL(tableName, sqlQuery string) ([]map[string]interface{}, error) {
	// Safety: only allow SELECT for read-only queries through this endpoint
	trimmed := strings.TrimSpace(strings.ToUpper(sqlQuery))
	if !strings.HasPrefix(trimmed, "SELECT") {
		return nil, fmt.Errorf("only SELECT queries are allowed through this endpoint")
	}
	return s.execQuery(sqlQuery)
}

func (s *Store) execQuery(query string, args ...interface{}) ([]map[string]interface{}, error) {
	rows, err := s.tablesDB.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("executing query: %w", err)
	}
	defer rows.Close()

	columns, _ := rows.Columns()
	var results []map[string]interface{}

	for rows.Next() {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}
		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}
		row := make(map[string]interface{})
		for i, col := range columns {
			row[col] = values[i]
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

func (s *Store) UpdateRow(tableName string, rowID int64, data map[string]interface{}) error {
	sqlName := "ut_" + tableName
	sets := []string{}
	vals := []interface{}{}

	for k, v := range data {
		sets = append(sets, k+" = ?")
		vals = append(vals, v)
	}
	sets = append(sets, "_modified_at = ?")
	vals = append(vals, time.Now().UTC().Format(time.RFC3339))
	vals = append(vals, rowID)

	query := fmt.Sprintf("UPDATE %s SET %s WHERE _rowid = ?", sqlName, strings.Join(sets, ", "))
	_, err := s.tablesDB.Exec(query, vals...)
	return err
}

func (s *Store) DeleteRow(tableName string, rowID int64) error {
	sqlName := "ut_" + tableName
	_, err := s.tablesDB.Exec(fmt.Sprintf("DELETE FROM %s WHERE _rowid = ?", sqlName), rowID)
	if err == nil {
		s.registryDB.Exec("UPDATE table_registry SET row_count = MAX(0, row_count - 1), modified_at = ? WHERE name = ?",
			time.Now().UTC().Format(time.RFC3339), tableName)
	}
	return err
}

func (s *Store) ImportCSV(tableName string, headers []string, rows [][]string) (int, error) {
	sqlName := "ut_" + tableName
	placeholders := strings.Repeat("?,", len(headers))
	placeholders = placeholders[:len(placeholders)-1]
	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", sqlName, strings.Join(headers, ","), placeholders)

	tx, err := s.tablesDB.Begin()
	if err != nil {
		return 0, err
	}
	stmt, err := tx.Prepare(query)
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("preparing insert: %w", err)
	}
	defer stmt.Close()

	count := 0
	for _, row := range rows {
		vals := make([]interface{}, len(row))
		for i, v := range row {
			vals[i] = v
		}
		if _, err := stmt.Exec(vals...); err != nil {
			tx.Rollback()
			return count, fmt.Errorf("inserting row %d: %w", count+1, err)
		}
		count++
	}

	if err := tx.Commit(); err != nil {
		return count, err
	}

	s.registryDB.Exec("UPDATE table_registry SET row_count = row_count + ?, modified_at = ? WHERE name = ?",
		count, time.Now().UTC().Format(time.RFC3339), tableName)
	return count, nil
}

func (s *Store) ExportJSON(tableName string) ([]map[string]interface{}, error) {
	return s.QueryRows(tableName, "", "", 0, 0)
}

// Delete drops a user table entirely.
func (s *Store) Delete(nameOrID string) error {
	t, err := s.Get(nameOrID)
	if err != nil || t == nil {
		return err
	}
	s.tablesDB.Exec(fmt.Sprintf("DROP TABLE IF EXISTS ut_%s", t.Name))
	s.registryDB.Exec("DELETE FROM table_columns WHERE table_id = ?", t.ID)
	_, err = s.registryDB.Exec("DELETE FROM table_registry WHERE id = ?", t.ID)
	return err
}

// Needed to avoid unused import
var _ = json.Marshal
