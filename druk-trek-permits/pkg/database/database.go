// Package database opens a PostgreSQL connection with a few retries, because
// when the whole system starts with `docker compose up` the database container
// may need a few seconds before it accepts connections.
package database

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/lib/pq" // PostgreSQL driver (registers itself as "postgres")
)

// Connect opens the database and pings it until it answers (max ~30 seconds).
func Connect(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}

	for attempt := 1; attempt <= 15; attempt++ {
		if err = db.Ping(); err == nil {
			log.Println("database: connected")
			return db, nil
		}
		log.Printf("database: not ready yet (attempt %d/15): %v", attempt, err)
		time.Sleep(2 * time.Second)
	}
	return nil, fmt.Errorf("database: could not connect: %w", err)
}
