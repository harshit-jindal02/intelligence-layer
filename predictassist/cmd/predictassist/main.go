// Command predictassist is the main PredictAssist server binary. It ingests
// OpenTelemetry data, stores it in ClickHouse, and exposes gRPC/REST APIs
// for anomaly detection and incident prediction.
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

func main() {
	// -----------------------------------------------------------------------
	// Logger
	// -----------------------------------------------------------------------
	logger, err := zap.NewProduction()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync() //nolint:errcheck

	// -----------------------------------------------------------------------
	// Configuration
	// -----------------------------------------------------------------------
	viper.SetConfigName("predictassist")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("configs")
	viper.AddConfigPath("/etc/predictassist")
	viper.AddConfigPath(".")

	if err := viper.ReadInConfig(); err != nil {
		logger.Fatal("failed to read config", zap.Error(err))
	}
	logger.Info("loaded configuration", zap.String("file", viper.ConfigFileUsed()))

	// -----------------------------------------------------------------------
	// Root context — cancelled on SIGINT / SIGTERM
	// -----------------------------------------------------------------------
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// -----------------------------------------------------------------------
	// ClickHouse
	// -----------------------------------------------------------------------
	chConn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{
			fmt.Sprintf("%s:%d",
				viper.GetString("storage.clickhouse.host"),
				viper.GetInt("storage.clickhouse.port"),
			),
		},
		Auth: clickhouse.Auth{
			Database: viper.GetString("storage.clickhouse.database"),
			Username: viper.GetString("storage.clickhouse.username"),
			Password: viper.GetString("storage.clickhouse.password"),
		},
		MaxOpenConns: viper.GetInt("storage.clickhouse.max_open_conns"),
		DialTimeout:  viper.GetDuration("storage.clickhouse.dial_timeout"),
	})
	if err != nil {
		logger.Fatal("failed to open ClickHouse connection", zap.Error(err))
	}
	if err := chConn.Ping(ctx); err != nil {
		logger.Fatal("failed to ping ClickHouse", zap.Error(err))
	}
	logger.Info("connected to ClickHouse",
		zap.String("host", viper.GetString("storage.clickhouse.host")),
		zap.Int("port", viper.GetInt("storage.clickhouse.port")),
	)

	// -----------------------------------------------------------------------
	// Redis
	// -----------------------------------------------------------------------
	rdb := redis.NewClient(&redis.Options{
		Addr:     viper.GetString("storage.redis.addr"),
		Password: viper.GetString("storage.redis.password"),
		DB:       viper.GetInt("storage.redis.db"),
		PoolSize: viper.GetInt("storage.redis.pool_size"),
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		logger.Fatal("failed to ping Redis", zap.Error(err))
	}
	logger.Info("connected to Redis", zap.String("addr", viper.GetString("storage.redis.addr")))

	// -----------------------------------------------------------------------
	// NATS (JetStream event bus)
	// -----------------------------------------------------------------------
	natsURL := viper.GetString("events.nats.url")
	nc, err := nats.Connect(natsURL,
		nats.MaxReconnects(viper.GetInt("events.nats.max_reconnects")),
		nats.ReconnectWait(viper.GetDuration("events.nats.reconnect_wait")),
		nats.Name(viper.GetString("events.nats.client_id")),
	)
	if err != nil {
		logger.Fatal("failed to connect to NATS", zap.Error(err))
	}
	logger.Info("connected to NATS", zap.String("url", natsURL))

	// Ensure JetStream context is available.
	js, err := nc.JetStream()
	if err != nil {
		logger.Fatal("failed to create JetStream context", zap.Error(err))
	}
	_ = js // Will be used by event publishers downstream.

	// -----------------------------------------------------------------------
	// OTLP gRPC receiver (TelemetrySource)
	// -----------------------------------------------------------------------
	otlpPort := viper.GetInt("ingestion.otlp.grpc_port")
	otlpLis, err := net.Listen("tcp", fmt.Sprintf(":%d", otlpPort))
	if err != nil {
		logger.Fatal("failed to listen on OTLP port", zap.Int("port", otlpPort), zap.Error(err))
	}
	otlpServer := grpc.NewServer()
	// TODO: Register OTLP trace/metric/log services once the OTLPReceiver
	// implementation of TelemetrySource is complete.
	go func() {
		logger.Info("OTLP gRPC receiver listening", zap.Int("port", otlpPort))
		if err := otlpServer.Serve(otlpLis); err != nil {
			logger.Error("OTLP server error", zap.Error(err))
		}
	}()

	// -----------------------------------------------------------------------
	// gRPC API server
	// -----------------------------------------------------------------------
	grpcPort := viper.GetInt("api.grpc_port")
	grpcLis, err := net.Listen("tcp", fmt.Sprintf(":%d", grpcPort))
	if err != nil {
		logger.Fatal("failed to listen on gRPC API port", zap.Int("port", grpcPort), zap.Error(err))
	}
	grpcServer := grpc.NewServer()
	// TODO: Register PredictAssist API services here.
	go func() {
		logger.Info("gRPC API server listening", zap.Int("port", grpcPort))
		if err := grpcServer.Serve(grpcLis); err != nil {
			logger.Error("gRPC API server error", zap.Error(err))
		}
	}()

	// -----------------------------------------------------------------------
	// REST gateway (health endpoint)
	// -----------------------------------------------------------------------
	restPort := viper.GetInt("api.rest_port")
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ok","ts":"%s"}`, time.Now().UTC().Format(time.RFC3339))
	})
	restServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", restPort),
		Handler:      mux,
		ReadTimeout:  viper.GetDuration("api.read_timeout"),
		WriteTimeout: viper.GetDuration("api.write_timeout"),
	}
	go func() {
		logger.Info("REST gateway listening", zap.Int("port", restPort))
		if err := restServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("REST gateway error", zap.Error(err))
		}
	}()

	// -----------------------------------------------------------------------
	// Log readiness
	// -----------------------------------------------------------------------
	logger.Info("PredictAssist server is ready",
		zap.Int("otlp_port", otlpPort),
		zap.Int("grpc_port", grpcPort),
		zap.Int("rest_port", restPort),
	)

	// -----------------------------------------------------------------------
	// Wait for shutdown signal
	// -----------------------------------------------------------------------
	sig := <-sigCh
	logger.Info("received shutdown signal", zap.String("signal", sig.String()))
	cancel()

	// Give in-flight work up to 15 seconds to finish.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	// Graceful shutdown in reverse dependency order.
	if err := restServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("REST gateway shutdown error", zap.Error(err))
	}
	grpcServer.GracefulStop()
	otlpServer.GracefulStop()

	nc.Drain() //nolint:errcheck
	if err := rdb.Close(); err != nil {
		logger.Error("Redis close error", zap.Error(err))
	}
	if err := chConn.Close(); err != nil {
		logger.Error("ClickHouse close error", zap.Error(err))
	}

	logger.Info("PredictAssist server stopped")
}
