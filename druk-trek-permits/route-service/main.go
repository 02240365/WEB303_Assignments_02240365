// =============================================================================
// ROUTE SERVICE (Service B) — owns trek routes
// -----------------------------------------------------------------------------
// Bounded context : "Route Catalogue" — which treks exist, how hard they
// are, how many people may go, and whether they are open.
// Database        : route_db (its own PostgreSQL instance)
// Exposed via     : gRPC on ROUTE_GRPC_PORT (default compose value 50051)
// =============================================================================
package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	pb "github.com/sonamzangmo/druk-trek-permits/gen/routepb"
	"github.com/sonamzangmo/druk-trek-permits/pkg/config"
	"github.com/sonamzangmo/druk-trek-permits/pkg/database"
)

func main() {
	log.SetPrefix("[route-service] ")

	// 1. Read configuration from environment variables.
	port := config.Require("ROUTE_GRPC_PORT")
	dsn := config.PostgresDSN()

	// 2. Connect to OUR OWN database and create the table.
	db, err := database.Connect(dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	repo := NewRouteRepository(db)
	if err := repo.Migrate(context.Background()); err != nil {
		log.Fatalf("migration failed: %v", err)
	}

	// 3. Start the gRPC server.
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("cannot listen on port %s: %v", port, err)
	}
	grpcServer := grpc.NewServer()
	pb.RegisterRouteServiceServer(grpcServer, NewRouteServer(repo))
	healthpb.RegisterHealthServer(grpcServer, health.NewServer()) // standard health check
	reflection.Register(grpcServer)                               // lets grpcurl/Postman discover the API

	// 4. Stop cleanly on Ctrl+C / `docker compose stop`.
	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
		<-stop
		log.Println("shutting down...")
		grpcServer.GracefulStop()
	}()

	log.Printf("gRPC server listening on :%s", port)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("gRPC server error: %v", err)
	}
}
