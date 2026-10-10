// =============================================================================
// PERMIT SERVICE (Service A) — owns trek permits
// -----------------------------------------------------------------------------
// Bounded context : "Permit Issuing" — who is trekking, on which route,
// when, and with how many people.
// Database        : permit_db (its own PostgreSQL instance)
// Depends on      : Route Service (gRPC, with timeout + retry + circuit breaker)
// Exposed via     : gRPC on PERMIT_GRPC_PORT (default compose value 50052)
// =============================================================================
package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	pb "github.com/sonamzangmo/druk-trek-permits/gen/permitpb"
	"github.com/sonamzangmo/druk-trek-permits/pkg/config"
	"github.com/sonamzangmo/druk-trek-permits/pkg/database"
)

func main() {
	log.SetPrefix("[permit-service] ")

	// 1. Configuration — all from environment variables.
	port := config.Require("PERMIT_GRPC_PORT")
	routeAddr := config.Require("ROUTE_SERVICE_ADDR") // e.g. route-service:50051
	resilience := ResilienceConfig{
		Timeout:          config.Duration("ROUTE_CALL_TIMEOUT", 2*time.Second),
		MaxRetries:       config.Int("ROUTE_MAX_RETRIES", 2),
		BaseBackoff:      config.Duration("ROUTE_RETRY_BACKOFF", 200*time.Millisecond),
		FailureThreshold: config.Int("ROUTE_CB_FAILURES", 3),
		OpenTimeout:      config.Duration("ROUTE_CB_OPEN_TIMEOUT", 15*time.Second),
	}
	log.Printf("resilience settings: %+v", resilience)

	// 2. Our OWN database (permit_db).
	db, err := database.Connect(config.PostgresDSN())
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	repo := NewPermitRepository(db)
	if err := repo.Migrate(context.Background()); err != nil {
		log.Fatalf("migration failed: %v", err)
	}

	// 3. Resilient client for the Route Service.
	routes, err := NewRouteClient(routeAddr, resilience)
	if err != nil {
		log.Fatalf("cannot create route-service client: %v", err)
	}

	// 4. gRPC server.
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("cannot listen on port %s: %v", port, err)
	}
	grpcServer := grpc.NewServer()
	pb.RegisterPermitServiceServer(grpcServer, NewPermitServer(repo, routes))
	healthpb.RegisterHealthServer(grpcServer, health.NewServer())
	reflection.Register(grpcServer)

	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
		<-stop
		log.Println("shutting down...")
		grpcServer.GracefulStop()
	}()

	log.Printf("gRPC server listening on :%s (route-service at %s)", port, routeAddr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("gRPC server error: %v", err)
	}
}
