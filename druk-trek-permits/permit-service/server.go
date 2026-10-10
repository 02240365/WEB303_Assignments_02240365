package main

// =============================================================================
// server.go — gRPC HANDLERS of the Permit Service
// -----------------------------------------------------------------------------
// Business rules for issuing a permit (checked by calling the Route Service):
//   * the route must EXIST                     -> otherwise NOT_FOUND
//   * the route must be OPEN                   -> otherwise FAILED_PRECONDITION
//   * group_size <= route.max_group_size       -> otherwise FAILED_PRECONDITION
//   * Route Service down / circuit open        -> UNAVAILABLE (nothing is saved)
//
// When READING a permit, live route details are added. If the Route Service is
// down, the permit is still returned with a FALLBACK route section
// ("available": false) instead of failing the whole request.
// =============================================================================

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/lib/pq"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/sonamzangmo/druk-trek-permits/gen/permitpb"
	routepb "github.com/sonamzangmo/druk-trek-permits/gen/routepb"
	"github.com/sonamzangmo/druk-trek-permits/pkg/validate"
)

// PermitServer implements pb.PermitServiceServer (generated from permit.proto).
type PermitServer struct {
	pb.UnimplementedPermitServiceServer
	repo   *PermitRepository
	routes *RouteClient // the ONLY way we get route data
}

func NewPermitServer(repo *PermitRepository, routes *RouteClient) *PermitServer {
	return &PermitServer{repo: repo, routes: routes}
}

// ----------------------------------------------------------------------------
// Validation
// ----------------------------------------------------------------------------

var passportPattern = regexp.MustCompile(`^[A-Z0-9]{6,9}$`)

// permitFields groups the editable fields so Create and Update share one validator.
type permitFields struct {
	RouteID, TrekkerName, Passport, Nationality, StartDate, Status *string
	GroupSize                                                      int32
}

// validatePermit trims/normalises the input and returns INVALID_ARGUMENT
// listing every problem found. Nothing is stored if this fails.
func validatePermit(f permitFields) error {
	*f.RouteID = strings.TrimSpace(*f.RouteID)
	*f.TrekkerName = strings.TrimSpace(*f.TrekkerName)
	*f.Passport = strings.ToUpper(strings.TrimSpace(*f.Passport))
	*f.Nationality = strings.TrimSpace(*f.Nationality)
	*f.StartDate = strings.TrimSpace(*f.StartDate)

	var errs validate.Errors
	if !validate.IsUUID(*f.RouteID) {
		errs.Add("route_id must be a valid UUID")
	}
	validate.Required(&errs, "trekker_name", *f.TrekkerName, 100)
	if !passportPattern.MatchString(*f.Passport) {
		errs.Add("passport_number must be 6-9 letters or digits")
	}
	validate.Required(&errs, "nationality", *f.Nationality, 60)
	validate.Range(&errs, "group_size", f.GroupSize, 1, 30)

	// start_date: correct format, not in the past, at most one year ahead.
	start, err := time.Parse("2006-01-02", *f.StartDate)
	if err != nil {
		errs.Add("start_date must be in YYYY-MM-DD format")
	} else {
		today := time.Now().UTC().Truncate(24 * time.Hour)
		if start.Before(today) {
			errs.Add("start_date cannot be in the past")
		} else if start.After(today.AddDate(1, 0, 0)) {
			errs.Add("start_date must be within the next 12 months")
		}
	}

	// Status is only sent on update.
	if f.Status != nil {
		*f.Status = strings.ToUpper(strings.TrimSpace(*f.Status))
		if *f.Status != "ISSUED" && *f.Status != "CANCELLED" {
			errs.Add("status must be ISSUED or CANCELLED")
		}
	}

	if !errs.Empty() {
		return status.Error(codes.InvalidArgument, errs.Error())
	}
	return nil
}

// checkRouteRules asks the Route Service about the route and applies the
// business rules. Errors coming back are already gRPC status errors.
func (s *PermitServer) checkRouteRules(ctx context.Context, routeID string, groupSize int32, mustBeOpen bool) error {
	route, err := s.routes.GetRoute(ctx, routeID)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return status.Errorf(codes.NotFound, "route %s does not exist", routeID)
		}
		return err // UNAVAILABLE / DEADLINE_EXCEEDED from the resilient client
	}
	if mustBeOpen && !route.IsOpen {
		return status.Errorf(codes.FailedPrecondition, "route %q is currently closed for trekking", route.Name)
	}
	if groupSize > route.MaxGroupSize {
		return status.Errorf(codes.FailedPrecondition,
			"group_size %d exceeds the maximum of %d for route %q", groupSize, route.MaxGroupSize, route.Name)
	}
	return nil
}

// toStatus converts repository errors into gRPC status errors.
func toStatus(err error) error {
	if errors.Is(err, ErrNotFound) {
		return status.Error(codes.NotFound, "permit not found")
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" { // unique_violation
		return status.Error(codes.AlreadyExists,
			"this passport already has a permit for this route on this start date")
	}
	log.Printf("permit-service: database error: %v", err)
	return status.Error(codes.Internal, "internal database error")
}

func checkID(id string) error {
	if !validate.IsUUID(id) {
		return status.Error(codes.InvalidArgument, "id must be a valid UUID")
	}
	return nil
}

// ----------------------------------------------------------------------------
// CRUD handlers
// ----------------------------------------------------------------------------

// CreatePermit: validate -> confirm route via Route Service -> save.
func (s *PermitServer) CreatePermit(ctx context.Context, req *pb.CreatePermitRequest) (*pb.Permit, error) {
	if err := validatePermit(permitFields{&req.RouteId, &req.TrekkerName, &req.PassportNumber,
		&req.Nationality, &req.StartDate, nil, req.GroupSize}); err != nil {
		return nil, err
	}
	if err := s.checkRouteRules(ctx, req.RouteId, req.GroupSize, true); err != nil {
		return nil, err // route missing/closed/service down -> nothing saved
	}

	p, err := s.repo.Create(ctx, req)
	if err != nil {
		return nil, toStatus(err)
	}
	log.Printf("permit-service: issued permit %s for route %s", p.Id, p.RouteId)
	s.attachRoute(ctx, p)
	return p, nil
}

// GetPermit: read from our DB, then enrich with live route details (with fallback).
func (s *PermitServer) GetPermit(ctx context.Context, req *pb.GetPermitRequest) (*pb.Permit, error) {
	if err := checkID(req.Id); err != nil {
		return nil, err
	}
	p, err := s.repo.Get(ctx, req.Id)
	if err != nil {
		return nil, toStatus(err)
	}
	s.attachRoute(ctx, p)
	return p, nil
}

// ListPermits returns permits from our own DB (optionally for one route) and
// attaches live route details from the Route Service.
// Many permits share the same route, so each DISTINCT route is fetched only
// once per request (10 permits on 2 routes = 2 gRPC calls, not 10).
// If the Route Service is down, the same fallback as GetPermit is used.
func (s *PermitServer) ListPermits(ctx context.Context, req *pb.ListPermitsRequest) (*pb.ListPermitsResponse, error) {
	if req.RouteId != "" && !validate.IsUUID(req.RouteId) {
		return nil, status.Error(codes.InvalidArgument, "route_id filter must be a valid UUID")
	}
	permits, err := s.repo.List(ctx, req.RouteId)
	if err != nil {
		return nil, toStatus(err)
	}

	cache := map[string]*pb.RouteInfo{} // route_id -> route details for this request
	for _, p := range permits {
		info, ok := cache[p.RouteId]
		if !ok {
			info = s.lookupRoute(ctx, p.RouteId)
			cache[p.RouteId] = info
		}
		p.Route = info
	}
	return &pb.ListPermitsResponse{Permits: permits}, nil
}

// UpdatePermit: validate -> re-check route rules -> save.
// A CANCELLED permit may stay on a closed route, so "open" is only enforced for ISSUED.
func (s *PermitServer) UpdatePermit(ctx context.Context, req *pb.UpdatePermitRequest) (*pb.Permit, error) {
	if err := checkID(req.Id); err != nil {
		return nil, err
	}
	if err := validatePermit(permitFields{&req.RouteId, &req.TrekkerName, &req.PassportNumber,
		&req.Nationality, &req.StartDate, &req.Status, req.GroupSize}); err != nil {
		return nil, err
	}
	if err := s.checkRouteRules(ctx, req.RouteId, req.GroupSize, req.Status == "ISSUED"); err != nil {
		return nil, err
	}

	p, err := s.repo.Update(ctx, req)
	if err != nil {
		return nil, toStatus(err)
	}
	log.Printf("permit-service: updated permit %s", p.Id)
	s.attachRoute(ctx, p)
	return p, nil
}

func (s *PermitServer) DeletePermit(ctx context.Context, req *pb.DeletePermitRequest) (*pb.DeletePermitResponse, error) {
	if err := checkID(req.Id); err != nil {
		return nil, err
	}
	if err := s.repo.Delete(ctx, req.Id); err != nil {
		return nil, toStatus(err)
	}
	log.Printf("permit-service: deleted permit %s", req.Id)
	return &pb.DeletePermitResponse{Message: fmt.Sprintf("permit %s deleted", req.Id)}, nil
}

// attachRoute adds live route details to a single permit.
func (s *PermitServer) attachRoute(ctx context.Context, p *pb.Permit) {
	p.Route = s.lookupRoute(ctx, p.RouteId)
}

// lookupRoute asks the Route Service for one route and turns the answer into
// a RouteInfo. It never fails:
// FALLBACK: if the Route Service cannot answer, the permit is still returned,
// with the route section marked as unavailable instead of failing the request.
func (s *PermitServer) lookupRoute(ctx context.Context, routeID string) *pb.RouteInfo {
	route, err := s.routes.GetRoute(ctx, routeID)
	switch {
	case err == nil:
		return routeInfo(route)
	case status.Code(err) == codes.NotFound:
		return &pb.RouteInfo{Available: false, Note: "route no longer exists in route-service"}
	default:
		log.Printf("permit-service: FALLBACK used for route %s: %v", routeID, err)
		return &pb.RouteInfo{Available: false,
			Note: "route details temporarily unavailable (route-service down); permit data is still valid"}
	}
}

func routeInfo(r *routepb.Route) *pb.RouteInfo {
	return &pb.RouteInfo{Name: r.Name, Region: r.Region, Difficulty: r.Difficulty,
		DurationDays: r.DurationDays, Available: true}
}
