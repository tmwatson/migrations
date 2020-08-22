package migrations_test

import (
	"database/sql"
	"fmt"
	"os"
	"testing"

	"github.com/sbowman/migrations"
)

const (
	// TableExists queries for the table in the PostgreSQL metadata.
	TableExists = `
select exists 
    (select from information_schema.tables 
            where table_schema = 'public' and 
                  table_name = $1)`
)

var conn *sql.DB

func TestMain(m *testing.M) {
	var err error

	migrations.Log = new(migrations.NilLogger)

	conn, err = sql.Open("postgres", "postgres://postgres@localhost/migrations_test?sslmode=disable")
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Unable to connect to migrations_test database: %s\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// TestUp confirms upward bound migrations work.
func TestUp(t *testing.T) {
	defer clean(t)

	if err := migrate(1); err != nil {
		t.Fatalf("Unable to run migration: %s", err)
	}

	if err := tableExists("schema_migrations"); err != nil {
		t.Fatal("The schema_migrations table wasn't created")
	}

	if err := tableExists("samples"); err != nil {
		t.Fatal("Sample table not found in database")
	}

	if _, err := conn.Exec("insert into samples (name) values ('Bob')"); err != nil {
		t.Errorf("Unable to insert record into samples: %s", err)
	}

	rows, err := conn.Query("select name from samples where name = 'Bob'")
	if err != nil {
		t.Errorf("Didn't find expected record in database: %s", err)
	}

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Errorf("Failed to get name from database: %s", err)
		}

		if name != "Bob" {
			t.Errorf("Expected name Bob, got %s", name)
		}
	}
}

// Make sure revisions, i.e. partial migrations, are working.
func TestRevisions(t *testing.T) {
	defer clean(t)

	if err := migrate(1); err != nil {
		t.Fatalf("Unable to run migration to revision 1: %s", err)
	}

	if _, err := conn.Exec("insert into samples (name, email) values ('Bob', 'bob@home.com')"); err == nil {
		t.Error("Expected inserting an email address to fail")
	}

	if err := migrate(2); err != nil {
		t.Fatalf("Unable to run migration to revision 2: %s", err)
	}

	if _, err := conn.Exec("insert into samples (name, email) values ('Bob', 'bob@home.com')"); err != nil {
		t.Errorf("Expected to be able to insert email address after revision 2: %s", err)
	}

	rows, err := conn.Query("select email from samples where name = 'Bob'")
	if err != nil {
		t.Errorf("Didn't find expected record in database: %s", err)
	}

	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			t.Errorf("Failed to get name from database: %s", err)
		}

		if email != "bob@home.com" {
			t.Errorf("Expected email bob@home.com for Bob, got %s", email)
		}
	}
}

// Make sure migrations can be rolled back.
func TestDown(t *testing.T) {
	defer clean(t)

	if err := migrate(2); err != nil {
		t.Fatalf("Unable to run migration to revision 2: %s", err)
	}

	if _, err := conn.Exec("insert into samples (name, email) values ('Bob', 'bob@home.com')"); err != nil {
		t.Errorf("Expected to be able to insert email address after revision 2: %s", err)
	}

	rows, err := conn.Query("select email from samples where name = 'Bob'")
	if err != nil {
		t.Errorf("Didn't find expected record in database: %s", err)
	}

	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			t.Errorf("Failed to get name from database: %s", err)
		}

		if email != "bob@home.com" {
			t.Errorf("Expected email bob@home.com for Bob, got %s", email)
		}
	}

	// Rollback
	if err := migrate(1); err != nil {
		t.Fatalf("Unable to run migration to revision 1: %s", err)
	}

	if _, err := conn.Exec("insert into samples (name, email) values ('Alice', 'alice@home.com')"); err == nil {
		t.Error("Expected inserting an email address to fail")
	}

	_, err = conn.Query("select email from samples where name = 'Bob'")
	if err == nil {
		t.Error("Expected an error, as the email column shouldn't exist")
	}

	rows, err = conn.Query("select name from samples where name = 'Alice'")
	if err != nil {
		t.Errorf("Unable to query for samples: %s", err)
	}

	for rows.Next() {
		t.Errorf("Did not expect results from the query")
	}
}

// Is the simplified Rollback function working?
func TestRollback(t *testing.T) {
	defer clean(t)

	if err := migrate(2); err != nil {
		t.Fatalf("Unable to run migration to revision 2: %s", err)
	}

	if _, err := conn.Exec("insert into samples (name, email) values ('Bob', 'bob@home.com')"); err != nil {
		t.Errorf("Expected insert to succeed: %s", err)
	}

	if err := migrations.Rollback(conn, "./sql", 1); err != nil {
		t.Fatalf("Unable to rollback migration to revision 1: %s", err)
	}

	_, err := conn.Query("select email from samples where name = 'Bob'")
	if err == nil {
		t.Error("Expected querying for the rolled-back column to fail")
	}
}

// Under normal circumstances, if part of a migration fails, the whole migration false.
func TestTransactions(t *testing.T) {
	defer clean(t)

	if err := migrate(2); err != nil {
		t.Fatalf("Unable to run migration to revision 2: %s", err)
	}

	// Skip migration 3
	if _, err := conn.Exec("insert into schema_migrations values ('3-no-tx.sql')"); err != nil {
		t.Fatalf("Failed to skip 3-no-tx.sql migration: %s", err)
	}

	if err := migrate(4); err == nil {
		t.Error("Expected migration 4 to fail")
	}

	rows, err := conn.Query("select name from samples where name = 'abc'")
	if err != nil {
		t.Fatalf("Unable to query for sample names:%s", err)
	}

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Errorf("Unable to scan results: %s", err)
			continue
		}

		if name == "abc" {
			t.Error("Unexpected abc value")
		}
	}
}

// Does the /notx flag run the migration outside a transaction?
func TestNoTxFlag(t *testing.T) {
	defer clean(t)

	if err := migrate(3); err == nil {
		t.Error("Expected the /notx migration to generate an error")
	}

	rows, err := conn.Query("select name from samples where name = 'abc'")
	if err != nil {
		t.Errorf("Unable to query for samples: %s", err)
	}

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Error("Unable to scan result")
		}

		if name != "abc" {
			t.Errorf("Expected abc, got %s", name)
		}

		if name == "zzz" {
			t.Error("Didn't expect the zzz record to get inserted")
		}
	}

	// Make sure the migration didn't "succeed"
	rows, err = conn.Query("select migration from schema_migrations")
	if err != nil {
		t.Errorf("Unable to query for migrations: %s", err)
	}

	var count int
	for rows.Next() {
		var migration string
		if err := rows.Scan(&migration); err != nil {
			t.Errorf("Unable to get migration data: %s", err)
			continue
		}

		count++

		if migration == "1-create-sample.sql" || migration == "2-add-email-to-sample.sql" {
			continue
		}

		t.Errorf("Didn't expect migration %s", migration)
	}

	if count != 2 {
		t.Errorf("Expected two migrations; found %d", count)
	}
}

// Shortcut to run the test migrations in the sql directory.
func migrate(revision int) error {
	return migrations.Migrate(conn, "./sql", revision)
}

// Clean out the database.
func clean(t *testing.T) {
	if _, err := conn.Exec("delete from schema_migrations"); err != nil {
		t.Fatalf("Unable to clear the schema_migrations table: %s", err)
	}

	rows, err := conn.Query("select table_name from information_schema.tables where table_schema='public'")
	if err != nil {
		t.Fatalf("Couldn't query for table names: %s", err)
	}

	var name string
	for rows.Next() {
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("Failed to get table name: %s", err)
		}

		// Note: not exactly safe, but this is just a test case
		if _, err := conn.Exec("drop table if exists " + name); err != nil {
			t.Fatalf("Couldn't drop table %s: %s", name, err)
		}
	}
}

// Check if the table exists.  Returns nil if the table exists.
func tableExists(table string) error {
	rows, err := conn.Query(TableExists, table)
	if err != nil {
		return err
	}

	if rows.Next() {
		var found bool
		if err := rows.Scan(&found); err != nil {
			return err
		}

		if found {
			return nil
		}
	}

	return sql.ErrNoRows
}
