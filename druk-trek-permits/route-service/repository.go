package main

// =============================================================================
// repository.go — DATABASE LAYER of the Route Service
// -----------------------------------------------------------------------------
// This is the ONLY code that touches route_db. The Permit Service has no access
// to this database; it must ask through gRPC (Database per Service pattern).
// =============================================================================

import (
	"context"
	"database/sql"
	"errors"
	"time"

	pb "github.com/sonamzangmo/druk-trek-permits/gen/routepb"
)

// ErrNotFound is returned when no route has the requested ID.
var ErrNotFound = errors.New("route not found")

// RouteRepository wraps the SQL queries for the "routes" table.
type RouteRepository struct {
	db *sql.DB
}

func NewRouteRepository(db *sql.DB) *RouteRepository {
	return &RouteRepository{db: db}
}

// Migrate creates the table if it does not exist yet (runs at startup).
func (r *RouteRepository) Migrate(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS routes (
			id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name           VARCHAR(100) NOT NULL UNIQUE,
			region         VARCHAR(60)  NOT NULL,
			difficulty     VARCHAR(10)  NOT NULL,
			duration_days  INT          NOT NULL,
			max_group_size INT          NOT NULL,
			is_open        BOOLEAN      NOT NULL DEFAULT TRUE,
			created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW()
		)`)
	return err
}

// columns used by every SELECT / RETURNING so scanRoute stays in sync.
const routeColumns = `id, name, region, difficulty, duration_days, max_group_size, is_open, created_at`

// scanRoute converts one database row into a protobuf Route message.
func scanRoute(row interface{ Scan(...any) error }) (*pb.Route, error) {
	var rt pb.Route
	var created time.Time
	err := row.Scan(&rt.Id, &rt.Name, &rt.Region, &rt.Difficulty,
		&rt.DurationDays, &rt.MaxGroupSize, &rt.IsOpen, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rt.CreatedAt = created.UTC().Format(time.RFC3339)
	return &rt, nil
}

func (r *RouteRepository) Create(ctx context.Context, in *pb.CreateRouteRequest) (*pb.Route, error) {
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO routes (name, region, difficulty, duration_days, max_group_size, is_open)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+routeColumns,
		in.Name, in.Region, in.Difficulty, in.DurationDays, in.MaxGroupSize, in.IsOpen)
	return scanRoute(row)
}

func (r *RouteRepository) Get(ctx context.Context, id string) (*pb.Route, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+routeColumns+` FROM routes WHERE id = $1`, id)
	return scanRoute(row)
}

func (r *RouteRepository) List(ctx context.Context) ([]*pb.Route, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+routeColumns+` FROM routes ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	routes := []*pb.Route{}
	for rows.Next() {
		rt, err := scanRoute(rows)
		if err != nil {
			return nil, err
		}
		routes = append(routes, rt)
	}
	return routes, rows.Err()
}

func (r *RouteRepository) Update(ctx context.Context, in *pb.UpdateRouteRequest) (*pb.Route, error) {
	row := r.db.QueryRowContext(ctx, `
		UPDATE routes
		SET name = $2, region = $3, difficulty = $4, duration_days = $5,
		    max_group_size = $6, is_open = $7
		WHERE id = $1
		RETURNING `+routeColumns,
		in.Id, in.Name, in.Region, in.Difficulty, in.DurationDays, in.MaxGroupSize, in.IsOpen)
	return scanRoute(row)
}

func (r *RouteRepository) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM routes WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
