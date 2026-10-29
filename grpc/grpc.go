// Package wtgrpc instruments gRPC: spans via the OTel stats handlers, plus interceptors that
// capture server panics and server/client errors that indicate a bug (Unknown, Internal, DataLoss).
package wtgrpc

import (
	"context"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	watchtower "github.com/watchtowerobservability/watchtower-go"
)

// ServerOptions are the options to pass to grpc.NewServer.
func ServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(UnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(StreamServerInterceptor()),
	}
}

// DialOptions are the options to pass to grpc.NewClient.
func DialOptions() []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		grpc.WithChainUnaryInterceptor(UnaryClientInterceptor()),
		grpc.WithChainStreamInterceptor(StreamClientInterceptor()),
	}
}

func reportable(err error) bool {
	switch status.Code(err) {
	case codes.Unknown, codes.Internal, codes.DataLoss:
		return true
	}
	return false
}

var errInternal = status.Error(codes.Internal, "internal error")

// UnaryServerInterceptor captures panics (answered as Internal) and reportable handler errors.
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if rec := recover(); rec != nil {
				watchtower.CapturePanic(ctx, rec)
				resp, err = nil, errInternal
			}
		}()
		resp, err = h(ctx, req)
		if reportable(err) {
			watchtower.CaptureError(ctx, err)
		}
		return resp, err
	}
}

// StreamServerInterceptor is the streaming counterpart of UnaryServerInterceptor.
func StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, h grpc.StreamHandler) (err error) {
		ctx := ss.Context()
		defer func() {
			if rec := recover(); rec != nil {
				watchtower.CapturePanic(ctx, rec)
				err = errInternal
			}
		}()
		err = h(srv, ss)
		if reportable(err) {
			watchtower.CaptureError(ctx, err)
		}
		return err
	}
}

// UnaryClientInterceptor captures reportable errors on the caller's span.
func UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, inv grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		err := inv(ctx, method, req, reply, cc, opts...)
		if reportable(err) {
			watchtower.CaptureError(ctx, err)
		}
		return err
	}
}

// StreamClientInterceptor captures reportable errors raised when the stream is opened.
func StreamClientInterceptor() grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, s grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		cs, err := s(ctx, desc, cc, method, opts...)
		if reportable(err) {
			watchtower.CaptureError(ctx, err)
		}
		return cs, err
	}
}
