package db

import "testing"

func TestRewriteForPostgres(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no placeholders", "SELECT 1", "SELECT 1"},
		{"single placeholder", "SELECT * FROM users WHERE id = ?", "SELECT * FROM users WHERE id = $1"},
		{"multiple placeholders", "INSERT INTO x VALUES (?, ?, ?)", "INSERT INTO x VALUES ($1, $2, $3)"},
		{"placeholder inside quotes — preserved", "SELECT * FROM x WHERE name = '? friend'", "SELECT * FROM x WHERE name = '? friend'"},
		{"mixed", "UPDATE x SET note = '? mark', y = ? WHERE z = ?", "UPDATE x SET note = '? mark', y = $1 WHERE z = $2"},
		{"trailing IN list", "SELECT 1 FROM t WHERE id IN (?,?,?,?,?,?,?,?,?,?,?,?)", "SELECT 1 FROM t WHERE id IN ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)"},
		{"DDL no placeholders", `CREATE TABLE x (id BIGSERIAL PRIMARY KEY)`, `CREATE TABLE x (id BIGSERIAL PRIMARY KEY)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rewriteForPostgres(tc.in)
			if got != tc.want {
				t.Errorf("\nin:   %s\ngot:  %s\nwant: %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestStoreQRespectsDialect(t *testing.T) {
	sl := &Store{Dialect: DialectSQLite}
	pg := &Store{Dialect: DialectPostgres}

	q := "SELECT * FROM x WHERE a = ? AND b = ?"
	if got := sl.q(q); got != q {
		t.Errorf("sqlite should pass through, got %q", got)
	}
	if got := pg.q(q); got != "SELECT * FROM x WHERE a = $1 AND b = $2" {
		t.Errorf("postgres rewrite wrong, got %q", got)
	}
}
