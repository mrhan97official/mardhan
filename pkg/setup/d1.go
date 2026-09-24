// Package setup inspects and applies the checked-in, additive D1 schema.
package setup

import (
  "fmt"
  "sort"
  "strings"

  "devcontrol/pkg/d1"
)

type Table struct {
  Name string `json:"name"`
  Columns []string `json:"columns"`
  Missing []string `json:"missing"`
  Mismatched []string `json:"mismatched"`
  Extra []string `json:"extra"`
  Exists bool `json:"exists"`
}

type Status struct {
  Connected bool `json:"connected"`
  Ready bool `json:"ready"`
  Tables []Table `json:"tables"`
  MissingIndexes []string `json:"missing_indexes"`
  Migrations []string `json:"migrations"`
}

const appSchemaVersion = "app-1.0.19"

// Prepare avoids replaying the full schema before every new deployment.
// The Databases page always performs a fresh inspection and can repair drift.
func Prepare() error {
  rows, err := d1.Query(`SELECT version FROM schema_migrations WHERE version = ? LIMIT 1`, appSchemaVersion)
  if err == nil && len(rows) == 1 { return nil }
  _, err = Ensure()
  return err
}

func Inspect() (Status, error) {
  status := Status{Tables: []Table{}, MissingIndexes: []string{}, Migrations: []string{}}
  rows, err := d1.Query(`SELECT name, type FROM sqlite_schema WHERE type IN ('table', 'index')`)
  if err != nil { return status, fmt.Errorf("koneksi D1 gagal: %w", err) }
  status.Connected = true
  found := map[string]bool{}
  for _, row := range rows {
    name, _ := row["name"].(string)
    found[name] = true
  }
  names := make([]string, 0, len(expectedColumns))
  for name := range expectedColumns { names = append(names, name) }
  sort.Strings(names)
  status.Ready = true
  for _, name := range names {
    table := Table{Name: name, Exists: found[name], Columns: []string{}, Missing: []string{}, Mismatched: []string{}, Extra: []string{}}
    if !table.Exists {
      table.Missing = append(table.Missing, expectedColumns[name]...)
      status.Ready = false
    } else {
      columns, queryErr := d1.Query(`PRAGMA table_info(` + name + `)`)
      if queryErr != nil { return Status{}, fmt.Errorf("gagal memeriksa %s: %w", name, queryErr) }
      present, expected := map[string]bool{}, map[string]bool{}
      for _, column := range expectedColumns[name] { expected[column] = true }
      for _, column := range columns {
        columnName, _ := column["name"].(string)
        columnType, _ := column["type"].(string)
        table.Columns = append(table.Columns, columnName)
        present[columnName] = true
        if !expected[columnName] { table.Extra = append(table.Extra, columnName) }
        if expected[columnName] && !strings.EqualFold(columnType, expectedTypes[name][columnName]) {
          table.Mismatched = append(table.Mismatched, columnName+" ("+columnType+" ≠ "+expectedTypes[name][columnName]+")")
          status.Ready = false
        }
      }
      for _, column := range expectedColumns[name] {
        if !present[column] { table.Missing = append(table.Missing, column); status.Ready = false }
      }
    }
    status.Tables = append(status.Tables, table)
  }
  for _, index := range expectedIndexes {
    if !found[index] { status.MissingIndexes = append(status.MissingIndexes, index); status.Ready = false }
  }
  if found["schema_migrations"] {
    applied, queryErr := d1.Query(`SELECT version FROM schema_migrations ORDER BY version`)
    if queryErr != nil { return Status{}, queryErr }
    for _, row := range applied {
      if version, ok := row["version"].(string); ok { status.Migrations = append(status.Migrations, version) }
    }
  }
  return status, nil
}

// Ensure only adds checked-in tables, indexes and columns. No old data is erased.
// Called by an admin action or at the start of an authenticated deployment.
func Ensure() (Status, error) {
  // Tables must exist before applying a legacy ALTER TABLE. CREATE IF NOT EXISTS
  // does not change existing columns, so old databases are preserved.
  for _, sql := range schemaStatements {
    if !strings.HasPrefix(sql, "CREATE TABLE") { continue }
    if _, err := d1.Query(sql); err != nil { return Status{}, fmt.Errorf("membuat tabel: %w", err) }
  }
  for _, statement := range migrationStatements {
    if strings.HasPrefix(statement.SQL, "ALTER TABLE") {
      parts := strings.Fields(statement.SQL)
      if len(parts) < 7 || (parts[2] != "services" && parts[2] != "project_thumbnails") { return Status{}, fmt.Errorf("migrasi kolom tidak dikenal") }
      table := parts[2]
      column := parts[5]
      columns, err := d1.Query(`PRAGMA table_info(` + table + `)`)
      if err != nil { return Status{}, err }
      exists := false
      for _, item := range columns { if item["name"] == column { exists = true } }
      if !exists {
        if _, err := d1.Query(statement.SQL); err != nil {
          // Two admins may prepare simultaneously; recheck before declaring failure.
          updated, checkErr := d1.Query(`PRAGMA table_info(` + table + `)`)
          if checkErr != nil { return Status{}, err }
          confirmed := false
          for _, item := range updated { if item["name"] == column { confirmed = true } }
          if !confirmed { return Status{}, fmt.Errorf("menambah %s: %w", column, err) }
        }
      }
    } else if _, err := d1.Query(statement.SQL); err != nil {
      return Status{}, fmt.Errorf("migrasi %s: %w", statement.Version, err)
    }
    if _, err := d1.Query(`INSERT OR IGNORE INTO schema_migrations (version) VALUES (?)`, statement.Version); err != nil {
      return Status{}, fmt.Errorf("mencatat migrasi: %w", err)
    }
  }
  for _, sql := range schemaStatements {
    if !strings.HasPrefix(sql, "CREATE INDEX") && !strings.HasPrefix(sql, "CREATE UNIQUE INDEX") { continue }
    if _, err := d1.Query(sql); err != nil { return Status{}, fmt.Errorf("membuat indeks: %w", err) }
  }
  status, err := Inspect()
  if err != nil { return status, err }
  if !status.Ready { return status, fmt.Errorf("struktur D1 masih berbeda; periksa tabel dan kolom yang ditampilkan") }
  if _, err := d1.Query(`INSERT OR IGNORE INTO schema_migrations (version) VALUES (?)`, appSchemaVersion); err != nil {
    return status, fmt.Errorf("mencatat versi skema: %w", err)
  }
  return status, nil
}
