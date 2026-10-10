package main

// =============================================================================
// handlers.go — REST endpoints of the API Gateway
// -----------------------------------------------------------------------------
// Each handler: read JSON / path params  ->  call a service over gRPC
//               ->  write JSON with the correct HTTP status code.
// The gateway contains NO business logic; services own their rules and data.
// =============================================================================

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	permitpb "github.com/sonamzangmo/druk-trek-permits/gen/permitpb"
	routepb "github.com/sonamzangmo/druk-trek-permits/gen/routepb"
)

// Gateway holds the gRPC clients for both services.
type Gateway struct {
	routes  routepb.RouteServiceClient
	permits permitpb.PermitServiceClient
	timeout time.Duration // max time for one whole request
}

// protobuf -> JSON options: snake_case names and include zero values
// (so "is_open": false is shown instead of being left out).
var jsonOut = protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

// ctx gives every request a deadline so the gateway never hangs.
func (g *Gateway) ctx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), g.timeout)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeProto writes a protobuf message as JSON.
func writeProto(w http.ResponseWriter, code int, m proto.Message) {
	b, err := jsonOut.Marshal(m)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codes.Internal, "could not encode response")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(b)
}

// readBody decodes the JSON request body into a protobuf request message.
// Malformed JSON, wrong types or unknown fields are rejected with 400.
func readBody(w http.ResponseWriter, r *http.Request, m proto.Message) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MB max
	if err != nil || len(body) == 0 {
		writeError(w, http.StatusBadRequest, codes.InvalidArgument, "request body is required (JSON)")
		return false
	}
	if err := protojson.Unmarshal(body, m); err != nil {
		writeError(w, http.StatusBadRequest, codes.InvalidArgument, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

// ----------------------------------------------------------------------------
// /health
// ----------------------------------------------------------------------------

func (g *Gateway) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "api-gateway"})
}

// ----------------------------------------------------------------------------
// /routes  ->  Route Service
// ----------------------------------------------------------------------------

func (g *Gateway) createRoute(w http.ResponseWriter, r *http.Request) {
	req := &routepb.CreateRouteRequest{}
	if !readBody(w, r, req) {
		return
	}
	ctx, cancel := g.ctx(r)
	defer cancel()
	res, err := g.routes.CreateRoute(ctx, req)
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	w.Header().Set("Location", "/routes/"+res.Id)
	writeProto(w, http.StatusCreated, res)
}

func (g *Gateway) listRoutes(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := g.ctx(r)
	defer cancel()
	res, err := g.routes.ListRoutes(ctx, &routepb.ListRoutesRequest{})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeProto(w, http.StatusOK, res)
}

func (g *Gateway) getRoute(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := g.ctx(r)
	defer cancel()
	res, err := g.routes.GetRoute(ctx, &routepb.GetRouteRequest{Id: r.PathValue("id")})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeProto(w, http.StatusOK, res)
}

func (g *Gateway) updateRoute(w http.ResponseWriter, r *http.Request) {
	req := &routepb.UpdateRouteRequest{}
	if !readBody(w, r, req) {
		return
	}
	req.Id = r.PathValue("id") // the URL decides which route is updated
	ctx, cancel := g.ctx(r)
	defer cancel()
	res, err := g.routes.UpdateRoute(ctx, req)
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeProto(w, http.StatusOK, res)
}

func (g *Gateway) deleteRoute(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := g.ctx(r)
	defer cancel()
	res, err := g.routes.DeleteRoute(ctx, &routepb.DeleteRouteRequest{Id: r.PathValue("id")})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeProto(w, http.StatusOK, res)
}

// GET /routes/{id}/permits — all permits for one route (served by Permit Service).
func (g *Gateway) listPermitsForRoute(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := g.ctx(r)
	defer cancel()
	res, err := g.permits.ListPermits(ctx, &permitpb.ListPermitsRequest{RouteId: r.PathValue("id")})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeProto(w, http.StatusOK, res)
}

// ----------------------------------------------------------------------------
// /permits  ->  Permit Service (which calls Route Service internally)
// ----------------------------------------------------------------------------

func (g *Gateway) createPermit(w http.ResponseWriter, r *http.Request) {
	req := &permitpb.CreatePermitRequest{}
	if !readBody(w, r, req) {
		return
	}
	ctx, cancel := g.ctx(r)
	defer cancel()
	res, err := g.permits.CreatePermit(ctx, req)
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	w.Header().Set("Location", "/permits/"+res.Id)
	writeProto(w, http.StatusCreated, res)
}

// GET /permits  or  GET /permits?route_id=<uuid>
func (g *Gateway) listPermits(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := g.ctx(r)
	defer cancel()
	res, err := g.permits.ListPermits(ctx, &permitpb.ListPermitsRequest{RouteId: r.URL.Query().Get("route_id")})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeProto(w, http.StatusOK, res)
}

func (g *Gateway) getPermit(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := g.ctx(r)
	defer cancel()
	res, err := g.permits.GetPermit(ctx, &permitpb.GetPermitRequest{Id: r.PathValue("id")})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeProto(w, http.StatusOK, res)
}

func (g *Gateway) updatePermit(w http.ResponseWriter, r *http.Request) {
	req := &permitpb.UpdatePermitRequest{}
	if !readBody(w, r, req) {
		return
	}
	req.Id = r.PathValue("id")
	ctx, cancel := g.ctx(r)
	defer cancel()
	res, err := g.permits.UpdatePermit(ctx, req)
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeProto(w, http.StatusOK, res)
}

func (g *Gateway) deletePermit(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := g.ctx(r)
	defer cancel()
	res, err := g.permits.DeletePermit(ctx, &permitpb.DeletePermitRequest{Id: r.PathValue("id")})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeProto(w, http.StatusOK, res)
}
