package wtgrpc

import (
	"context"
	"net"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// panicky is a health server whose Check panics, so no generated code is needed.
type panicky struct{ grpc_health_v1.UnimplementedHealthServer }

func (panicky) Check(context.Context, *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	panic("kaboom")
}

func (panicky) Watch(*grpc_health_v1.HealthCheckRequest, grpc_health_v1.Health_WatchServer) error {
	return status.Error(codes.Internal, "stream failed")
}

func TestServerPanicAndStreamErrorAreCaptured(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(ServerOptions()...)
	grpc_health_v1.RegisterHealthServer(srv, panicky{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet", append(DialOptions(),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	c := grpc_health_v1.NewHealthClient(conn)

	if _, err := c.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{}); status.Code(err) != codes.Internal {
		t.Fatalf("panic must answer Internal, got %v", err)
	}
	w, err := c.Watch(context.Background(), &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Recv(); status.Code(err) != codes.Internal {
		t.Fatalf("stream error %v", err)
	}
	srv.Stop() // waits for handlers so their spans have ended

	var events int
	for _, s := range sr.Ended() {
		events += len(s.Events())
	}
	if events < 2 {
		t.Fatalf("want >=2 exception events (panic + stream error), got %d", events)
	}
}

func TestReportable(t *testing.T) {
	if reportable(nil) || reportable(status.Error(codes.NotFound, "x")) || !reportable(status.Error(codes.Internal, "x")) {
		t.Fatal("unexpected reportable classification")
	}
}
