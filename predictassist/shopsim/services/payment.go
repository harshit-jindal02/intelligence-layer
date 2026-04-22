package services

import (
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

var paymentTracer = otel.Tracer("shopsim/payment")

// PaymentService handles payment processing.
type PaymentService struct {
	db             *pgxpool.Pool
	logger         *zap.Logger
	requestCount   metric.Int64Counter
	requestLatency metric.Float64Histogram
}

// ProcessPaymentRequest is the payload for POST /payments/process.
type ProcessPaymentRequest struct {
	OrderID int     `json:"order_id" binding:"required"`
	Amount  float64 `json:"amount" binding:"required"`
}

// NewPaymentService creates a new PaymentService with OTel metrics initialised.
func NewPaymentService(db *pgxpool.Pool, logger *zap.Logger) (*PaymentService, error) {
	meter := otel.Meter("shopsim/payment")

	reqCount, err := meter.Int64Counter("payment.request.count",
		metric.WithDescription("Total number of payment requests"),
	)
	if err != nil {
		return nil, fmt.Errorf("creating request count metric: %w", err)
	}

	reqLatency, err := meter.Float64Histogram("payment.request.latency_ms",
		metric.WithDescription("Payment request latency in milliseconds"),
	)
	if err != nil {
		return nil, fmt.Errorf("creating request latency metric: %w", err)
	}

	return &PaymentService{
		db:             db,
		logger:         logger,
		requestCount:   reqCount,
		requestLatency: reqLatency,
	}, nil
}

// RegisterRoutes wires up payment endpoints on the given router.
func (s *PaymentService) RegisterRoutes(router *gin.Engine) {
	router.POST("/payments/process", s.processPayment)
}

func (s *PaymentService) processPayment(c *gin.Context) {
	ctx, span := paymentTracer.Start(c.Request.Context(), "ProcessPayment")
	defer span.End()
	start := time.Now()

	var req ProcessPaymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.requestCount.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "bad_request")))
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	span.SetAttributes(
		attribute.Int("payment.order_id", req.OrderID),
		attribute.Float64("payment.amount", req.Amount),
	)

	// Simulate payment processing with random latency (50-500ms).
	processingTime := time.Duration(50+rand.Intn(450)) * time.Millisecond
	s.logger.Info("processing payment",
		zap.Int("order_id", req.OrderID),
		zap.Float64("amount", req.Amount),
		zap.Duration("simulated_latency", processingTime),
	)
	time.Sleep(processingTime)

	// Simulate occasional payment failures (~5% failure rate).
	if rand.Float64() < 0.05 {
		s.logger.Warn("payment declined",
			zap.Int("order_id", req.OrderID),
			zap.Float64("amount", req.Amount),
		)
		s.requestCount.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "declined")))

		// Record payment as failed.
		_, _ = s.db.Exec(ctx,
			"INSERT INTO payments (order_id, amount, status) VALUES ($1, $2, 'declined')",
			req.OrderID, req.Amount,
		)

		c.JSON(http.StatusOK, gin.H{"status": "declined", "order_id": req.OrderID})
		return
	}

	// Record successful payment.
	var paymentID int
	err := s.db.QueryRow(ctx,
		"INSERT INTO payments (order_id, amount, status) VALUES ($1, $2, 'completed') RETURNING id",
		req.OrderID, req.Amount,
	).Scan(&paymentID)
	if err != nil {
		s.logger.Error("payment record insert failed", zap.Error(err))
		s.requestCount.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "error")))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record payment"})
		return
	}

	elapsed := float64(time.Since(start).Milliseconds())
	s.requestLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String("endpoint", "POST /payments/process")))
	s.requestCount.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "ok")))

	s.logger.Info("payment completed",
		zap.Int("payment_id", paymentID),
		zap.Int("order_id", req.OrderID),
	)

	c.JSON(http.StatusOK, gin.H{
		"status":     "completed",
		"payment_id": paymentID,
		"order_id":   req.OrderID,
	})
}
