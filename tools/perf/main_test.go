package main

import "testing"

func TestPerformanceDatabaseBoundary(t *testing.T) {
	for _, dsn := range []string{"postgres://u:perf@127.0.0.1/production", "postgres://perf.example/production", "postgres://127.0.0.1/pastime", "postgres://127.0.0.1/pastime_perf?dbname=production"} {
		if validPerfDatabase(dsn) == nil {
			t.Errorf("accepted %s", dsn)
		}
	}
	if err := validPerfDatabase("postgres://u:p@127.0.0.1/pastime_perf?sslmode=disable"); err != nil {
		t.Fatal(err)
	}
}
