package ingestion

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"sync"

	"github.com/harshitjindal/predictassist/internal/models"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
)

const (
	// DefaultOTLPPort is the standard OTLP gRPC port.
	DefaultOTLPPort = 4317

	// DefaultChannelBuffer is the default capacity for telemetry channels.
	DefaultChannelBuffer = 4096
)

// OTLPReceiverConfig holds configuration for the OTLP gRPC receiver.
type OTLPReceiverConfig struct {
	// Port is the gRPC listen port. Defaults to 4317.
	Port int

	// ChannelBuffer is the capacity of internal telemetry channels.
	// Defaults to 4096.
	ChannelBuffer int
}

// OTLPReceiver implements TelemetrySource by running an OTLP gRPC server that
// accepts logs, metrics, and trace data from OpenTelemetry collectors and SDKs.
type OTLPReceiver struct {
	cfg    OTLPReceiverConfig
	server *grpc.Server

	logsCh    chan *models.LogRecord
	metricsCh chan *models.MetricRecord
	tracesCh  chan *models.TraceSpan

	mu      sync.Mutex
	started bool
}

// NewOTLPReceiver creates a new OTLP gRPC receiver with the given configuration.
// If cfg is nil, default values are used.
func NewOTLPReceiver(cfg *OTLPReceiverConfig) *OTLPReceiver {
	c := OTLPReceiverConfig{
		Port:          DefaultOTLPPort,
		ChannelBuffer: DefaultChannelBuffer,
	}
	if cfg != nil {
		if cfg.Port > 0 {
			c.Port = cfg.Port
		}
		if cfg.ChannelBuffer > 0 {
			c.ChannelBuffer = cfg.ChannelBuffer
		}
	}

	return &OTLPReceiver{
		cfg:       c,
		logsCh:    make(chan *models.LogRecord, c.ChannelBuffer),
		metricsCh: make(chan *models.MetricRecord, c.ChannelBuffer),
		tracesCh:  make(chan *models.TraceSpan, c.ChannelBuffer),
	}
}

// Start begins listening for OTLP gRPC connections. It registers the log,
// metric, and trace collector service endpoints and starts serving in a
// background goroutine. Start returns once the listener is bound or ctx is
// cancelled.
func (r *OTLPReceiver) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.started {
		return fmt.Errorf("otlp receiver already started")
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", r.cfg.Port))
	if err != nil {
		return fmt.Errorf("otlp receiver: listen on port %d: %w", r.cfg.Port, err)
	}

	r.server = grpc.NewServer()
	collogspb.RegisterLogsServiceServer(r.server, &logsServiceServer{receiver: r})
	colmetricspb.RegisterMetricsServiceServer(r.server, &metricsServiceServer{receiver: r})
	coltracepb.RegisterTraceServiceServer(r.server, &traceServiceServer{receiver: r})

	r.started = true

	go func() {
		if err := r.server.Serve(lis); err != nil {
			log.Printf("otlp receiver: grpc serve error: %v", err)
		}
	}()

	go func() {
		<-ctx.Done()
		r.server.GracefulStop()
	}()

	log.Printf("otlp receiver: listening on :%d", r.cfg.Port)
	return nil
}

// Stop gracefully shuts down the OTLP gRPC server and closes telemetry channels.
func (r *OTLPReceiver) Stop(_ context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started {
		return nil
	}

	if r.server != nil {
		r.server.GracefulStop()
	}

	close(r.logsCh)
	close(r.metricsCh)
	close(r.tracesCh)
	r.started = false

	log.Printf("otlp receiver: stopped")
	return nil
}

// Logs returns a read-only channel of ingested log records.
func (r *OTLPReceiver) Logs() <-chan *models.LogRecord {
	return r.logsCh
}

// Metrics returns a read-only channel of ingested metric records.
func (r *OTLPReceiver) Metrics() <-chan *models.MetricRecord {
	return r.metricsCh
}

// Traces returns a read-only channel of ingested trace spans.
func (r *OTLPReceiver) Traces() <-chan *models.TraceSpan {
	return r.tracesCh
}

// logsServiceServer implements the OTLP LogsService gRPC endpoint.
type logsServiceServer struct {
	collogspb.UnimplementedLogsServiceServer
	receiver *OTLPReceiver
}

// Export receives OTLP log data, normalizes it to internal models, and sends
// records to the logs channel.
func (s *logsServiceServer) Export(_ context.Context, req *collogspb.ExportLogsServiceRequest) (*collogspb.ExportLogsServiceResponse, error) {
	records := NormalizeLogs(req.GetResourceLogs())
	for i := range records {
		select {
		case s.receiver.logsCh <- &records[i]:
		default:
			log.Printf("otlp receiver: logs channel full, dropping record")
		}
	}
	return &collogspb.ExportLogsServiceResponse{}, nil
}

// metricsServiceServer implements the OTLP MetricsService gRPC endpoint.
type metricsServiceServer struct {
	colmetricspb.UnimplementedMetricsServiceServer
	receiver *OTLPReceiver
}

// Export receives OTLP metric data, normalizes it to internal models, and sends
// records to the metrics channel.
func (s *metricsServiceServer) Export(_ context.Context, req *colmetricspb.ExportMetricsServiceRequest) (*colmetricspb.ExportMetricsServiceResponse, error) {
	records := NormalizeMetrics(req.GetResourceMetrics())
	for i := range records {
		select {
		case s.receiver.metricsCh <- &records[i]:
		default:
			log.Printf("otlp receiver: metrics channel full, dropping record")
		}
	}
	return &colmetricspb.ExportMetricsServiceResponse{}, nil
}

// traceServiceServer implements the OTLP TraceService gRPC endpoint.
type traceServiceServer struct {
	coltracepb.UnimplementedTraceServiceServer
	receiver *OTLPReceiver
}

// Export receives OTLP trace data, normalizes it to internal models, and sends
// spans to the traces channel.
func (s *traceServiceServer) Export(_ context.Context, req *coltracepb.ExportTraceServiceRequest) (*coltracepb.ExportTraceServiceResponse, error) {
	spans := NormalizeTraces(req.GetResourceSpans())
	for i := range spans {
		select {
		case s.receiver.tracesCh <- &spans[i]:
		default:
			log.Printf("otlp receiver: traces channel full, dropping span")
		}
	}
	return &coltracepb.ExportTraceServiceResponse{}, nil
}

// hexEncode converts a byte slice to a hex-encoded string.
func hexEncode(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return hex.EncodeToString(b)
}
