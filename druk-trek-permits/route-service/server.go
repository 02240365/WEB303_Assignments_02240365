package main

// =============================================================================
// server.go — gRPC HANDLERS of the Route Service
// -----------------------------------------------------------------------------
// Each handler:  1) validates the request
//                2) calls the repository
//                3) turns Go errors into proper gRPC status codes, e.g.
//                   bad input        -> codes.InvalidArgument
//                   missing route    -> codes.NotFound
//                   duplicate name   -> codes.AlreadyExists
//                   database failure -> codes.Internal
// =============================================================================

import (
	"context"
	"errors"
	"log"
	"strings"

	"github.com/lib/pq"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/sonamzangmo/druk-trek-permits/gen/routepb"
	"github.com/sonamzangmo/druk-trek-permits/pkg/validate"
)

// RouteServer implements pb.RouteServiceServer (generated from route.proto).
type RouteServer struct {
	pb.UnimplementedRouteServiceServer
	repo *RouteRepository
}

func NewRouteServer(repo *RouteRepository) *RouteServer {
	return &RouteServer{repo: repo}
}

// allowed difficulty levels for a trek.
var difficulties = map[string]bool{"EASY": true, "MODERATE": true, "HARD": true}

// validateRouteFields checks the fields shared by Create and Update.
// Text is trimmed and difficulty upper-cased before checking.
func validateRouteFields(name, region, difficulty *string, days, group int32) error {
	*name = strings.TrimSpace(*name)
	*region = strings.TrimSpace(*region)
	*difficulty = strings.ToUpper(strings.TrimSpace(*difficulty))

	var errs validate.Errors
	validate.Required(&errs, "name", *name, 100)
	validate.Required(&errs, "region", *region, 60)
	if !difficulties[*difficulty] {
		errs.Add("difficulty must be one of EASY, MODERATE, HARD")
	}
	validate.Range(&errs, "duration_days", days, 1, 40)
	validate.Range(&errs, "max_group_size", group, 1, 30)

	if !errs.Empty() {
		return status.Error(codes.InvalidArgument, errs.Error())
	}
	return nil
}

// checkID rejects badly formatted IDs before they reach the database.
func checkID(id string) error {
	if !validate.IsUUID(id) {
		return status.Error(codes.InvalidArgument, "id must be a valid UUID")
	}
	return nil
}

// toStatus converts a repository error into a gRPC status error.
func toStatus(err error) error {
	if errors.Is(err, ErrNotFound) {
		return status.Error(codes.NotFound, "route not found")
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" { // unique_violation
		return status.Error(codes.AlreadyExists, "a route with this name already exists")
	}
	log.Printf("route-service: database error: %v", err)
	return status.Error(codes.Internal, "internal database error")
}

func (s *RouteServer) CreateRoute(ctx context.Context, req *pb.CreateRouteRequest) (*pb.Route, error) {
	if err := validateRouteFields(&req.Name, &req.Region, &req.Difficulty, req.DurationDays, req.MaxGroupSize); err != nil {
		return nil, err // invalid data is NEVER stored
	}
	rt, err := s.repo.Create(ctx, req)
	if err != nil {
		return nil, toStatus(err)
	}
	log.Printf("route-service: created route %s (%s)", rt.Id, rt.Name)
	return rt, nil
}

func (s *RouteServer) GetRoute(ctx context.Context, req *pb.GetRouteRequest) (*pb.Route, error) {
	if err := checkID(req.Id); err != nil {
		return nil, err
	}
	log.Printf("route-service: GetRoute %s", req.Id)
	rt, err := s.repo.Get(ctx, req.Id)
	if err != nil {
		return nil, toStatus(err)
	}
	return rt, nil
}

func (s *RouteServer) ListRoutes(ctx context.Context, _ *pb.ListRoutesRequest) (*pb.ListRoutesResponse, error) {
	routes, err := s.repo.List(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ListRoutesResponse{Routes: routes}, nil
}

func (s *RouteServer) UpdateRoute(ctx context.Context, req *pb.UpdateRouteRequest) (*pb.Route, error) {
	if err := checkID(req.Id); err != nil {
		return nil, err
	}
	if err := validateRouteFields(&req.Name, &req.Region, &req.Difficulty, req.DurationDays, req.MaxGroupSize); err != nil {
		return nil, err
	}
	rt, err := s.repo.Update(ctx, req)
	if err != nil {
		return nil, toStatus(err)
	}
	log.Printf("route-service: updated route %s", rt.Id)
	return rt, nil
}

func (s *RouteServer) DeleteRoute(ctx context.Context, req *pb.DeleteRouteRequest) (*pb.DeleteRouteResponse, error) {
	if err := checkID(req.Id); err != nil {
		return nil, err
	}
	if err := s.repo.Delete(ctx, req.Id); err != nil {
		return nil, toStatus(err)
	}
	log.Printf("route-service: deleted route %s", req.Id)
	return &pb.DeleteRouteResponse{Message: "route " + req.Id + " deleted"}, nil
}
