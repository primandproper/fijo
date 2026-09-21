package api

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/primandproper/fijo/internal/auth/interceptor"
	"github.com/primandproper/fijo/internal/auth/principal"
	ownerauthgrpc "github.com/primandproper/fijo/internal/services/ownerauth/grpc"
	ownersgrpc "github.com/primandproper/fijo/internal/services/owners/grpc"

	auditgrpc "github.com/primandproper/platform-go/v14/audit/grpc"
	signingrpc "github.com/primandproper/platform-go/v14/authentication/signin/grpc"
	identitygrpc "github.com/primandproper/platform-go/v14/identity/grpc"
	authzgrpc "github.com/primandproper/primitives-go/v2/authorization/grpc"
	grpcerrors "github.com/primandproper/primitives-go/v2/errors/grpc"
	"github.com/primandproper/primitives-go/v2/observability"
	"github.com/primandproper/primitives-go/v2/observability/logging"

	"github.com/samber/do/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// Requirements declares what every method on the server requires: the
// permission each platform surface names for itself, Fijo's own surfaces, and
// the health service, which admits anybody because a probe has no token. A
// method not declared here is denied, which is how a new RPC that forgot its
// policy fails on its first call rather than mounting open.
func Requirements() (*authzgrpc.Requirements, error) {
	b := authzgrpc.NewRequirements()

	identitygrpc.Require(b)
	signingrpc.Require(b)
	auditgrpc.Require(b)
	ownerauthgrpc.Require(b)
	ownersgrpc.Require(b)

	for _, method := range []string{
		grpc_health_v1.Health_Check_FullMethodName,
		grpc_health_v1.Health_List_FullMethodName,
		grpc_health_v1.Health_Watch_FullMethodName,
	} {
		b.Public(method)
	}

	return b.Build()
}

// registerInterceptors registers the chains the gRPC server is built from:
// recovery, authentication, authorization, and the error encoder that turns a
// platform sentinel into a considered status code.
func registerInterceptors(i do.Injector) {
	do.Provide(i, func(_ do.Injector) (*authzgrpc.Requirements, error) {
		return Requirements()
	})

	do.Provide(i, func(i do.Injector) (*authzgrpc.Enforcer, error) {
		pillars, err := observability.InvokePillars(i)
		if err != nil {
			return nil, err
		}

		reqs, err := do.Invoke[*authzgrpc.Requirements](i)
		if err != nil {
			return nil, err
		}

		return authzgrpc.NewEnforcer(reqs, principal.Grants,
			authzgrpc.WithLogger(pillars.Logger),
			authzgrpc.WithMetricsProvider(pillars.MetricsProvider),
		)
	})

	do.Provide(i, func(i do.Injector) ([]grpc.UnaryServerInterceptor, error) {
		pillars, err := observability.InvokePillars(i)
		if err != nil {
			return nil, err
		}

		auth, err := do.Invoke[*interceptor.Authenticator](i)
		if err != nil {
			return nil, err
		}

		enforcer, err := do.Invoke[*authzgrpc.Enforcer](i)
		if err != nil {
			return nil, err
		}

		return []grpc.UnaryServerInterceptor{
			// Recovery is outermost so it catches panics from everything below it.
			recoverUnary(pillars.Logger),
			auth.UnaryServerInterceptor(),
			// After authentication, because it reads the grants that established.
			enforcer.UnaryServerInterceptor(),
			grpcerrors.UnaryErrorEncodingInterceptor(),
		}, nil
	})

	do.Provide(i, func(i do.Injector) ([]grpc.StreamServerInterceptor, error) {
		pillars, err := observability.InvokePillars(i)
		if err != nil {
			return nil, err
		}

		auth, err := do.Invoke[*interceptor.Authenticator](i)
		if err != nil {
			return nil, err
		}

		enforcer, err := do.Invoke[*authzgrpc.Enforcer](i)
		if err != nil {
			return nil, err
		}

		return []grpc.StreamServerInterceptor{
			recoverStream(pillars.Logger),
			auth.StreamServerInterceptor(),
			enforcer.StreamServerInterceptor(),
			grpcerrors.StreamErrorEncodingInterceptor(),
		}, nil
	})
}

// recoverUnary turns a panic in a handler into codes.Internal and a log line
// with the stack, so one nil dereference degrades into a 500 rather than
// taking the process with it.
func recoverUnary(logger logging.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				logger.WithValue("method", info.FullMethod).WithValue("stack", string(debug.Stack())).
					Error("recovered from panic in gRPC unary handler", fmt.Errorf("%v", r))
				err = status.Error(codes.Internal, "internal server error")
			}
		}()

		return handler(ctx, req)
	}
}

func recoverStream(logger logging.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer func() {
			if r := recover(); r != nil {
				logger.WithValue("method", info.FullMethod).WithValue("stack", string(debug.Stack())).
					Error("recovered from panic in gRPC stream handler", fmt.Errorf("%v", r))
				err = status.Error(codes.Internal, "internal server error")
			}
		}()

		return handler(srv, ss)
	}
}
