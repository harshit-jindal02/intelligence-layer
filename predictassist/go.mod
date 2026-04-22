module github.com/harshitjindal/predictassist

go 1.22

require (
	github.com/ClickHouse/clickhouse-go/v2 v2.26.0
	github.com/gin-gonic/gin v1.10.0
	github.com/jackc/pgx/v5 v5.6.0
	github.com/nats-io/nats.go v1.36.0
	github.com/redis/go-redis/v9 v9.6.1
	github.com/spf13/viper v1.19.0
	go.opentelemetry.io/otel v1.28.0
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc v0.4.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.28.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.28.0
	go.opentelemetry.io/otel/sdk v1.28.0
	go.opentelemetry.io/proto/otlp v1.3.1
	go.uber.org/zap v1.27.0
	gonum.org/v1/gonum v0.15.0
	google.golang.org/grpc v1.65.0
	google.golang.org/protobuf v1.34.2
)
