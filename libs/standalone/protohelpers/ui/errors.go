package ui

import (
	"errors"
	"fmt"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
)

// The page tells the two kinds of failure apart, because they are not the same
// news: something the user typed is theirs to correct, and something this could
// not do is ours.
//
// The distinction is carried as a gRPC status, which is also what decides how a
// failure reaches the browser. grpcurl returns a plain error from an Invoke to
// grpcui as an infrastructure failure, which grpcui answers with a 500 - so a
// mistyped number would be reported as the server having broken. A status error
// instead comes back as a failed RPC, rendered in the Response tab where the user
// can see what they did and fix it.
//
// So: user errors are statuses, system errors are not, and 500 is reserved for the
// second kind.

// userErrorf is a failure the caller can fix: a value that will not parse, a
// method that does not exist, a request that is not the shape the method takes.
func userErrorf(format string, args ...any) error {
	return status.Errorf(codes.InvalidArgument, format, args...)
}

// systemErrorf is a failure the caller cannot do anything about. Left as a plain
// error so it surfaces as one rather than as something the user got wrong.
func systemErrorf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

// fromCapability classifies what a capability answered with.
//
// A capability that failed is not the page failing: it was asked something and it
// answered. So neither outcome is a 500 from this package's point of view - but a
// capability already says whose fault it was, via Origin, and repeating that is
// better than flattening it. OriginUser becomes a user error, so the browser is
// told what to change; anything else is reported as the capability's own failure.
func fromCapability(err error) error {
	if err == nil {
		return nil
	}

	var capErr caperrors.Error
	if errors.As(err, &capErr) {
		if capErr.Origin() == caperrors.OriginUser {
			return status.Error(codes.InvalidArgument, capErr.Error())
		}
		return status.Error(codes.FailedPrecondition, capErr.Error())
	}

	// No origin to go on. Reported as a failed call rather than a broken page,
	// because the call is what failed: the request reached a capability and came
	// back with this.
	return status.Error(codes.Unknown, err.Error())
}

// isUserError reports whether err is one of ours from userErrorf.
//
// Only InvalidArgument counts. A capability answering InvalidArgument of its own
// accord is saying the same thing about the same request, so treating it the same
// way is right rather than convenient.
func isUserError(err error) bool {
	if err == nil {
		return false
	}
	var se interface{ GRPCStatus() *status.Status }
	if errors.As(err, &se) {
		return se.GRPCStatus().Code() == codes.InvalidArgument
	}
	return false
}

// httpStatus is the code a failure is answered with: the caller's mistake is a
// 400, and anything else is a 500 - which is what makes a 500 mean the page
// itself failed rather than that the request was wrong.
func httpStatus(err error) int {
	if isUserError(err) {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// writeError answers a request with the failure and the code that fits it.
func writeError(w http.ResponseWriter, err error) {
	message := err.Error()
	if s, ok := status.FromError(err); ok {
		// Without this the body reads "rpc error: code = InvalidArgument desc =
		// ...", which is the transport talking rather than the thing that went
		// wrong.
		message = s.Message()
	}
	http.Error(w, message, httpStatus(err))
}
