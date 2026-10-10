package main

// =============================================================================
// errors.go — maps gRPC status codes to HTTP status codes
// -----------------------------------------------------------------------------
// The services speak gRPC; Postman speaks HTTP. The gateway translates:
//
//   gRPC code             HTTP status                 Example
//   ------------------    -------------------------   ------------------------------
//   OK                    200 / 201                   success
//   INVALID_ARGUMENT      400 Bad Request             missing name, bad date
//   NOT_FOUND             404 Not Found               unknown route / permit id
//   ALREADY_EXISTS        409 Conflict                duplicate route name
//   FAILED_PRECONDITION   422 Unprocessable Entity    route closed, group too big
//   UNAVAILABLE           503 Service Unavailable     route-service stopped
//   DEADLINE_EXCEEDED     504 Gateway Timeout         service too slow
//   anything else         500 Internal Server Error
// =============================================================================

import (
	"net/http"

	rpccode "google.golang.org/genproto/googleapis/rpc/code"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func httpStatusFromGRPC(code codes.Code) int {
	switch code {
	case codes.OK:
		return http.StatusOK
	case codes.InvalidArgument, codes.OutOfRange:
		return http.StatusBadRequest
	case codes.NotFound:
		return http.StatusNotFound
	case codes.AlreadyExists:
		return http.StatusConflict
	case codes.FailedPrecondition:
		return http.StatusUnprocessableEntity
	case codes.Unavailable:
		return http.StatusServiceUnavailable
	case codes.DeadlineExceeded:
		return http.StatusGatewayTimeout
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	case codes.PermissionDenied:
		return http.StatusForbidden
	default:
		return http.StatusInternalServerError
	}
}

// errorBody is the JSON shape of every error the gateway returns:
//
//	{ "error": { "http_status": 404, "grpc_code": "NOT_FOUND", "message": "route not found" } }
type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	HTTPStatus int    `json:"http_status"`
	GRPCCode   string `json:"grpc_code"`
	Message    string `json:"message"`
}

// writeGRPCError converts a gRPC error from a service into an HTTP response.
func writeGRPCError(w http.ResponseWriter, err error) {
	st := status.Convert(err)
	code := httpStatusFromGRPC(st.Code())
	writeJSON(w, code, errorBody{errorDetail{code, codeName(st.Code()), st.Message()}})
}

// writeError is used for problems detected by the gateway itself (e.g. bad JSON).
func writeError(w http.ResponseWriter, httpCode int, grpcCode codes.Code, msg string) {
	writeJSON(w, httpCode, errorBody{errorDetail{httpCode, codeName(grpcCode), msg}})
}

// codeName returns the official upper-case name of a gRPC code, e.g. "NOT_FOUND".
func codeName(c codes.Code) string {
	return rpccode.Code(c).String()
}
