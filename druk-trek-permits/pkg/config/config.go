// Package config reads settings from ENVIRONMENT VARIABLES.
//
// Nothing like ports, hostnames or passwords is hard-coded in the services:
// everything comes from the environment (set in docker-compose.yml / .env).
package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"time"
)

// Require returns the value of an environment variable and stops the program
// with a clear message if it is missing. Used for settings that MUST be
// provided (ports, database connection details, service addresses).
func Require(key string) string {
	val := os.Getenv(key)
	if val == "" {
		log.Fatalf("config: required environment variable %s is not set", key)
	}
	return val
}

// Duration reads a duration such as "2s" or "300ms". If the variable is not
// set, the given fallback is used (these are tuning knobs, not connection info).
func Duration(key string, fallback time.Duration) time.Duration {
	val := os.Getenv(key)
	if val == "" {
		return fallback
	}
	d, err := time.ParseDuration(val)
	if err != nil {
		log.Fatalf("config: %s=%q is not a valid duration (example: 2s)", key, val)
	}
	return d
}

// Int reads an integer setting, falling back to a default when unset.
func Int(key string, fallback int) int {
	val := os.Getenv(key)
	if val == "" {
		return fallback
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		log.Fatalf("config: %s=%q is not a valid integer", key, val)
	}
	return n
}

// PostgresDSN builds a PostgreSQL connection string from DB_* variables.
func PostgresDSN() string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		Require("DB_HOST"), Require("DB_PORT"), Require("DB_USER"),
		Require("DB_PASSWORD"), Require("DB_NAME"))
}
