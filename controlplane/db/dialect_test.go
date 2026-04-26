package db

import "testing"

func TestDetectDialect(t *testing.T) {
	cases := []struct {
		dsn  string
		want Dialect
	}{
		{"./cp.db", DialectSQLite},
		{"/var/lib/okesu/cp.db", DialectSQLite},
		{"sqlite:///abs/path.db", DialectSQLite},
		{"postgres://localhost:5432/cp", DialectPostgres},
		{"postgresql://user:pass@db.example.com/cp?sslmode=require", DialectPostgres},
	}
	for _, tc := range cases {
		if got := detectDialect(tc.dsn); got != tc.want {
			t.Errorf("detectDialect(%q) = %s, want %s", tc.dsn, got, tc.want)
		}
	}
}
