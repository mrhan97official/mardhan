// Package databrowser lets an admin read D1 tables from the Databases page
// without opening the Cloudflare dashboard. It is strictly read-only:
// table and column names are only ever taken from D1's own schema, and
// every user value is passed as a bound parameter.
package databrowser

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"devcontrol/pkg/d1"
	"devcontrol/pkg/util"
)

type column struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	PK     bool   `json:"pk"`
	Masked bool   `json:"masked"`
}

type tableCount struct {
	Name string `json:"name"`
	Rows int64  `json:"rows"`
}

const maxCell = 4000

func quote(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// Secrets stay on the server even for the admin: hashes, tokens and signed
// deployment tickets are shown as masked values.
func sensitive(name string) bool {
	lower := strings.ToLower(name)
	for _, key := range []string{"hash", "secret", "password", "token", "ticket"} {
		if strings.Contains(lower, key) { return true }
	}
	return false
}

func tableNames() ([]string, error) {
	rows, err := d1.Query(`SELECT name FROM sqlite_schema WHERE type = 'table'
		AND name NOT LIKE 'sqlite_%' AND name NOT LIKE '_cf_%' AND name NOT LIKE 'd1_%' ORDER BY name`)
	if err != nil { return nil, err }
	names := make([]string, 0, len(rows))
	for _, row := range rows { if name, _ := row["name"].(string); name != "" { names = append(names, name) } }
	return names, nil
}

func toInt(value interface{}) int64 {
	switch number := value.(type) {
	case float64: return int64(number)
	case int64: return number
	case string: parsed, _ := strconv.ParseInt(number, 10, 64); return parsed
	}
	return 0
}

// countRows avoids compound SELECT (UNION ALL), which D1 limits to a few
// terms. It reads up to 10 counts per query as plain scalar subqueries:
//   SELECT (SELECT COUNT(*) FROM "a") AS c0, (SELECT COUNT(*) FROM "b") AS c1
// and falls back to one query per table if a batch is rejected.
func countRows(names []string) map[string]int64 {
	counts := make(map[string]int64, len(names))
	for start := 0; start < len(names); start += 10 {
		end := start + 10
		if end > len(names) { end = len(names) }
		batch := names[start:end]
		parts := make([]string, 0, len(batch))
		for index, name := range batch { parts = append(parts, fmt.Sprintf(`(SELECT COUNT(*) FROM %s) AS c%d`, quote(name), index)) }
		rows, err := d1.Query(`SELECT ` + strings.Join(parts, ", "))
		if err == nil && len(rows) == 1 {
			for index, name := range batch { counts[name] = toInt(rows[0][fmt.Sprintf("c%d", index)]) }
			continue
		}
		for _, name := range batch {
			single, singleErr := d1.Query(`SELECT COUNT(*) AS n FROM ` + quote(name))
			if singleErr == nil && len(single) == 1 { counts[name] = toInt(single[0]["n"]) } else { counts[name] = -1 }
		}
	}
	return counts
}

func listTables(w http.ResponseWriter) {
	names, err := tableNames()
	if err != nil { util.Error(w, http.StatusBadGateway, err); return }
	counts := countRows(names)
	result := make([]tableCount, 0, len(names))
	for _, name := range names { result = append(result, tableCount{Name: name, Rows: counts[name]}) }
	util.JSON(w, http.StatusOK, map[string]interface{}{"tables": result})
}

func browse(w http.ResponseWriter, r *http.Request, table string) {
	names, err := tableNames()
	if err != nil { util.Error(w, http.StatusBadGateway, err); return }
	known := false
	for _, name := range names { if name == table { known = true; break } }
	if !known { util.Error(w, http.StatusNotFound, fmt.Errorf("tabel %q tidak ditemukan", table)); return }

	info, err := d1.Query(`PRAGMA table_info(` + quote(table) + `)`)
	if err != nil { util.Error(w, http.StatusBadGateway, err); return }
	columns := make([]column, 0, len(info))
	for _, row := range info {
		name, _ := row["name"].(string)
		kind, _ := row["type"].(string)
		columns = append(columns, column{Name: name, Type: kind, PK: toInt(row["pk"]) > 0, Masked: sensitive(name)})
	}
	if len(columns) == 0 { util.Error(w, http.StatusBadGateway, fmt.Errorf("kolom tabel tidak terbaca")); return }

	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	if limit <= 0 { limit = 50 }
	if limit > 200 { limit = 200 }
	offset, _ := strconv.Atoi(query.Get("offset"))
	if offset < 0 { offset = 0 }

	// Search every non-masked column; the term is one bound parameter (?1).
	where, params := "", []interface{}{}
	if term := strings.TrimSpace(query.Get("q")); term != "" {
		if len(term) > 200 { term = term[:200] }
		clauses := []string{}
		for _, item := range columns {
			if !item.Masked { clauses = append(clauses, "CAST("+quote(item.Name)+" AS TEXT) LIKE ?1 ESCAPE '\\'") }
		}
		if len(clauses) > 0 {
			escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(term)
			where = " WHERE " + strings.Join(clauses, " OR ")
			params = append(params, "%"+escaped+"%")
		}
	}

	// Sort only by a real column; default: newest rows first.
	order := " ORDER BY rowid DESC"
	sortColumn := query.Get("sort")
	for _, item := range columns {
		if item.Name == sortColumn {
			direction := "ASC"
			if strings.EqualFold(query.Get("dir"), "desc") { direction = "DESC" }
			order = " ORDER BY " + quote(item.Name) + " " + direction
		}
	}

	countRows, err := d1.Query(`SELECT COUNT(*) AS n FROM `+quote(table)+where, params...)
	if err != nil { util.Error(w, http.StatusBadGateway, err); return }
	total := int64(0)
	if len(countRows) > 0 { total = toInt(countRows[0]["n"]) }

	selectSQL := `SELECT * FROM ` + quote(table) + where + order + fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
	rows, err := d1.Query(selectSQL, params...)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "rowid") {
		// WITHOUT ROWID tables: fall back to schema order.
		rows, err = d1.Query(strings.Replace(selectSQL, " ORDER BY rowid DESC", "", 1), params...)
	}
	if err != nil { util.Error(w, http.StatusBadGateway, err); return }

	for _, row := range rows {
		for _, item := range columns {
			value, ok := row[item.Name]
			if !ok || value == nil { continue }
			if item.Masked { row[item.Name] = "••••••"; continue }
			if text, isText := value.(string); isText && len(text) > maxCell { row[item.Name] = text[:maxCell] + "…" }
		}
	}
	util.JSON(w, http.StatusOK, map[string]interface{}{
		"table": table, "columns": columns, "rows": rows, "total": total, "offset": offset, "limit": limit,
	})
}

// Handle serves GET /api/databases?view=tables and ?table=<name>. It returns
// false when the request is not a data-browser request.
func Handle(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet { return false }
	query := r.URL.Query()
	if query.Get("view") == "tables" { listTables(w); return true }
	if table := query.Get("table"); table != "" { browse(w, r, table); return true }
	return false
}
