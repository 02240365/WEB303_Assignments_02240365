// =============================================================================
// API GATEWAY — the single REST entry point (REST in, gRPC out)
// -----------------------------------------------------------------------------
// Postman (or any client) only ever talks to this gateway on GATEWAY_PORT.
// The gateway forwards each request to the right microservice over gRPC and
// converts gRPC errors into proper HTTP status codes (see errors.go).
//
//	Method  URL                      Forwards to
//	------  -----------------------  -------------------------------
//	GET     /health                  (gateway itself)
//	POST    /routes                  RouteService.CreateRoute
//	GET     /routes                  RouteService.ListRoutes
//	GET     /routes/{id}             RouteService.GetRoute
//	PUT     /routes/{id}             RouteService.UpdateRoute
//	DELETE  /routes/{id}             RouteService.DeleteRoute
//	GET     /routes/{id}/permits     PermitService.ListPermits(route_id)
//	POST    /permits                 PermitService.CreatePermit
//	GET     /permits[?route_id=]     PermitService.ListPermits
//	GET     /permits/{id}            PermitService.GetPermit
//	PUT     /permits/{id}            PermitService.UpdatePermit
//	DELETE  /permits/{id}            PermitService.DeletePermit
//
// =============================================================================
package main

import (
	"log"
	"net/http"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	permitpb "github.com/sonamzangmo/druk-trek-permits/gen/permitpb"
	routepb "github.com/sonamzangmo/druk-trek-permits/gen/routepb"
	"github.com/sonamzangmo/druk-trek-permits/pkg/config"
)

func main() {
	log.SetPrefix("[api-gateway] ")

	// Configuration from environment variables.
	port := config.Require("GATEWAY_PORT")
	routeAddr := config.Require("ROUTE_SERVICE_ADDR")
	permitAddr := config.Require("PERMIT_SERVICE_ADDR")
	timeout := config.Duration("GATEWAY_REQUEST_TIMEOUT", 10*time.Second)

	// gRPC client connections (non-blocking; they reconnect automatically).
	routeConn, err := grpc.NewClient(routeAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("route-service client: %v", err)
	}
	permitConn, err := grpc.NewClient(permitAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("permit-service client: %v", err)
	}

	g := &Gateway{
		routes:  routepb.NewRouteServiceClient(routeConn),
		permits: permitpb.NewPermitServiceClient(permitConn),
		timeout: timeout,
	}

	// Resource-based URLs using Go 1.22+ method-aware routing.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", g.health)

	mux.HandleFunc("POST /routes", g.createRoute)
	mux.HandleFunc("GET /routes", g.listRoutes)
	mux.HandleFunc("GET /routes/{id}", g.getRoute)
	mux.HandleFunc("PUT /routes/{id}", g.updateRoute)
	mux.HandleFunc("DELETE /routes/{id}", g.deleteRoute)
	mux.HandleFunc("GET /routes/{id}/permits", g.listPermitsForRoute)

	mux.HandleFunc("POST /permits", g.createPermit)
	mux.HandleFunc("GET /permits", g.listPermits)
	mux.HandleFunc("GET /permits/{id}", g.getPermit)
	mux.HandleFunc("PUT /permits/{id}", g.updatePermit)
	mux.HandleFunc("DELETE /permits/{id}", g.deletePermit)

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           logRequests(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("REST gateway listening on :%s (routes -> %s, permits -> %s)", port, routeAddr, permitAddr)
	log.Fatal(server.ListenAndServe())
}

// statusRecorder remembers the status code so we can log it.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// logRequests prints one line per request: method, path, status, duration.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Printf("%s %s -> %d (%s)", r.Method, r.URL.RequestURI(), rec.status, time.Since(start).Round(time.Millisecond))
	})
}
