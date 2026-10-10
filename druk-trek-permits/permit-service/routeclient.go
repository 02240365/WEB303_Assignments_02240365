package main

// =============================================================================
// routeclient.go — RESILIENT gRPC client: Permit Service  ──►  Route Service
// -----------------------------------------------------------------------------
// Every call to the Route Service goes through THREE resilience patterns:
//
//   1. TIMEOUT          each attempt gets its own deadline (ROUTE_CALL_TIMEOUT),
//                       so a slow/hung Route Service can never block us forever.
//
//   2. RETRY + BACKOFF  if an attempt fails with a *temporary* error
//                       (UNAVAILABLE or DEADLINE_EXCEEDED) we wait and try again:
//                       200ms, 400ms, 800ms ... (exponential backoff).
//                       Business errors like NOT_FOUND are NOT retried —
//                       asking again would give the same answer.
//
//   3. CIRCUIT BREAKER  after ROUTE_CB_FAILURES consecutive failed calls the
//                       breaker OPENS: further calls fail immediately for
//                       ROUTE_CB_OPEN_TIMEOUT without touching the network.
//                       Then it goes HALF-OPEN and lets one test call through;
//                       success CLOSES it again, failure re-OPENS it.
//
//      ┌────────┐ failures ≥ N ┌──────┐  after timeout  ┌───────────┐
//      │ CLOSED │ ───────────► │ OPEN │ ──────────────► │ HALF-OPEN │
//      └────────┘ ◄─────────── └──────┘ ◄────────────── └───────────┘
//                  test call OK           test call fails
//
// Order of wrapping:   breaker( retry( timeout( gRPC call ) ) )
// =============================================================================

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/sony/gobreaker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	routepb "github.com/sonamzangmo/druk-trek-permits/gen/routepb"
)

// ResilienceConfig holds the tuning values (all read from env vars in main.go).
type ResilienceConfig struct {
	Timeout          time.Duration // per-attempt deadline
	MaxRetries       int           // extra attempts after the first one
	BaseBackoff      time.Duration // wait before 1st retry, doubled each time
	FailureThreshold int           // consecutive failures that open the breaker
	OpenTimeout      time.Duration // how long the breaker stays open
}

// RouteClient is what the permit handlers use to talk to the Route Service.
type RouteClient struct {
	client  routepb.RouteServiceClient
	breaker *gobreaker.CircuitBreaker
	cfg     ResilienceConfig
}

// NewRouteClient creates the gRPC connection and the circuit breaker.
// grpc.NewClient does NOT block, so the Permit Service starts even if the
// Route Service is down (it will simply fail fast and recover later).
func NewRouteClient(addr string, cfg ResilienceConfig) (*RouteClient, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}

	breaker := gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        "route-service",
		MaxRequests: 1,               // calls allowed while HALF-OPEN
		Timeout:     cfg.OpenTimeout, // OPEN -> HALF-OPEN after this
		ReadyToTrip: func(c gobreaker.Counts) bool {
			return c.ConsecutiveFailures >= uint32(cfg.FailureThreshold)
		},
		// Only infrastructure problems count as failures. A "route not found"
		// answer means the Route Service is HEALTHY, so it must not trip the breaker.
		IsSuccessful: func(err error) bool {
			return err == nil || !isInfraError(err)
		},
		OnStateChange: func(name string, from, to gobreaker.State) {
			log.Printf("CIRCUIT BREAKER [%s]: %s -> %s", name, from, to)
		},
	})

	return &RouteClient{client: routepb.NewRouteServiceClient(conn), breaker: breaker, cfg: cfg}, nil
}

// GetRoute fetches a route by ID with timeout + retry + circuit breaker.
// On failure it always returns a gRPC status error (never hangs, never panics).
func (c *RouteClient) GetRoute(ctx context.Context, id string) (*routepb.Route, error) {
	result, err := c.breaker.Execute(func() (any, error) {
		return c.getWithRetry(ctx, id)
	})

	// Breaker is OPEN (or HALF-OPEN and busy): fail fast with a clear message.
	if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
		log.Printf("circuit breaker OPEN: skipping call to route-service")
		return nil, status.Error(codes.Unavailable,
			"route-service is currently unavailable (circuit breaker open) - please try again shortly")
	}
	if err != nil {
		return nil, err
	}
	return result.(*routepb.Route), nil
}

// getWithRetry performs up to 1+MaxRetries attempts, each with its own timeout.
func (c *RouteClient) getWithRetry(ctx context.Context, id string) (*routepb.Route, error) {
	var lastErr error
	attempts := c.cfg.MaxRetries + 1

	for attempt := 1; attempt <= attempts; attempt++ {
		// --- TIMEOUT: fresh deadline for this attempt ---
		callCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
		route, err := c.client.GetRoute(callCtx, &routepb.GetRouteRequest{Id: id})
		cancel()

		if err == nil {
			return route, nil
		}
		lastErr = err

		// Business errors (NOT_FOUND, INVALID_ARGUMENT...) are returned at once.
		if !isRetryable(err) {
			return nil, err
		}
		if attempt == attempts {
			log.Printf("route-service call failed (attempt %d/%d): %s - giving up",
				attempt, attempts, status.Code(err))
			break
		}

		// --- RETRY with EXPONENTIAL BACKOFF: 1x, 2x, 4x the base wait ---
		wait := c.cfg.BaseBackoff * time.Duration(1<<(attempt-1))
		log.Printf("route-service call failed (attempt %d/%d): %s - retrying in %s",
			attempt, attempts, status.Code(err), wait)
		select {
		case <-time.After(wait):
		case <-ctx.Done(): // the caller (gateway) gave up — stop retrying
			return nil, status.Error(codes.DeadlineExceeded, "request cancelled while retrying route-service")
		}
	}

	log.Printf("last error from route-service: %v", lastErr)
	return nil, status.Errorf(codes.Unavailable,
		"route-service is unavailable (no response after %d attempts with backoff) - please try again later", attempts)
}

// isRetryable: only temporary network/timeout problems are worth retrying.
func isRetryable(err error) bool {
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded:
		return true
	}
	return false
}

// isInfraError: errors that mean "the Route Service is unhealthy".
func isInfraError(err error) bool {
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.Internal, codes.Unknown:
		return true
	}
	return false
}
