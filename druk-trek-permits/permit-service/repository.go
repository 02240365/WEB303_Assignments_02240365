package main

// =============================================================================
// repository.go — DATABASE LAYER of the Permit Service
// -----------------------------------------------------------------------------
// Only this file touches permit_db.
//
// NOTE: route_id is stored as a plain UUID, NOT a foreign key. Routes live in a
// different database owned by the Route Service, so referential checks are done
// through the Route Service's gRPC API instead (Database per Service pattern).
// =============================================================================

import (
	"context"
	"database/sql"
	"errors"
	"time"

	pb "github.com/sonamzangmo/druk-trek-permits/gen/permitpb"
)

var ErrNotFound = errors.New("permit not found")

type PermitRepository struct {
	db *sql.DB
}

func NewPermitRepository(db *sql.DB) *PermitRepository {
	return &PermitRepository{db: db}
}

// Migrate creates the permits table at startup.
// The UNIQUE constraint stops the same passport getting two permits for the
// same route on the same start date.
func (r *PermitRepository) Migrate(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS permits (
			id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			route_id        UUID         NOT NULL,
			trekker_name    VARCHAR(100) NOT NULL,
			passport_number VARCHAR(9)   NOT NULL,
			nationality     VARCHAR(60)  NOT NULL,
			group_size      INT          NOT NULL,
			start_date      DATE         NOT NULL,
			status          VARCHAR(10)  NOT NULL DEFAULT 'ISSUED',
			created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			UNIQUE (passport_number, route_id, start_date)
		)`)
	return err
}

const permitColumns = `id, route_id, trekker_name, passport_number, nationality,
	group_size, start_date, status, created_at`

// scanPermit converts one database row into a protobuf Permit message.
func scanPermit(row interface{ Scan(...any) error }) (*pb.Permit, error) {
	var p pb.Permit
	var start, created time.Time
	err := row.Scan(&p.Id, &p.RouteId, &p.TrekkerName, &p.PassportNumber,
		&p.Nationality, &p.GroupSize, &start, &p.Status, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.StartDate = start.Format("2006-01-02")
	p.CreatedAt = created.UTC().Format(time.RFC3339)
	return &p, nil
}

func (r *PermitRepository) Create(ctx context.Context, in *pb.CreatePermitRequest) (*pb.Permit, error) {
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO permits (route_id, trekker_name, passport_number, nationality, group_size, start_date)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+permitColumns,
		in.RouteId, in.TrekkerName, in.PassportNumber, in.Nationality, in.GroupSize, in.StartDate)
	return scanPermit(row)
}

func (r *PermitRepository) Get(ctx context.Context, id string) (*pb.Permit, error) {
	return scanPermit(r.db.QueryRowContext(ctx, `SELECT `+permitColumns+` FROM permits WHERE id = $1`, id))
}

// List returns all permits, or only those for one route if routeID is set.
func (r *PermitRepository) List(ctx context.Context, routeID string) ([]*pb.Permit, error) {
	query := `SELECT ` + permitColumns + ` FROM permits`
	args := []any{}
	if routeID != "" {
		query += ` WHERE route_id = $1`
		args = append(args, routeID)
	}
	query += ` ORDER BY created_at`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	permits := []*pb.Permit{}
	for rows.Next() {
		p, err := scanPermit(rows)
		if err != nil {
			return nil, err
		}
		permits = append(permits, p)
	}
	return permits, rows.Err()
}

func (r *PermitRepository) Update(ctx context.Context, in *pb.UpdatePermitRequest) (*pb.Permit, error) {
	row := r.db.QueryRowContext(ctx, `
		UPDATE permits
		SET route_id = $2, trekker_name = $3, passport_number = $4, nationality = $5,
		    group_size = $6, start_date = $7, status = $8
		WHERE id = $1
		RETURNING `+permitColumns,
		in.Id, in.RouteId, in.TrekkerName, in.PassportNumber, in.Nationality,
		in.GroupSize, in.StartDate, in.Status)
	return scanPermit(row)
}

func (r *PermitRepository) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM permits WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
