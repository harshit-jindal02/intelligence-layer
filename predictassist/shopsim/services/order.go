package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

var orderTracer = otel.Tracer("shopsim/order")

// OrderService handles order-related HTTP operations.
type OrderService struct {
	db             *pgxpool.Pool
	logger         *zap.Logger
	paymentURL     string
	requestCount   metric.Int64Counter
	requestLatency metric.Float64Histogram
}

// Order represents an order entity.
type Order struct {
	ID        int     `json:"id"`
	UserID    int     `json:"user_id"`
	ProductID int     `json:"product_id"`
	Quantity  int     `json:"quantity"`
	Total     float64 `json:"total"`
	Status    string  `json:"status"`
}

// CreateOrderRequest is the payload for POST /orders.
type CreateOrderRequest struct {
	UserID    int `json:"user_id" binding:"required"`
	ProductID int `json:"product_id" binding:"required"`
	Quantity  int `json:"quantity" binding:"required"`
}

// NewOrderService creates a new OrderService with OTel metrics initialised.
func NewOrderService(db *pgxpool.Pool, logger *zap.Logger, paymentURL string) (*OrderService, error) {
	meter := otel.Meter("shopsim/order")

	reqCount, err := meter.Int64Counter("order.request.count",
		metric.WithDescription("Total number of order requests"),
	)
	if err != nil {
		return nil, fmt.Errorf("creating request count metric: %w", err)
	}

	reqLatency, err := meter.Float64Histogram("order.request.latency_ms",
		metric.WithDescription("Order request latency in milliseconds"),
	)
	if err != nil {
		return nil, fmt.Errorf("creating request latency metric: %w", err)
	}

	return &OrderService{
		db:             db,
		logger:         logger,
		paymentURL:     paymentURL,
		requestCount:   reqCount,
		requestLatency: reqLatency,
	}, nil
}

// RegisterRoutes wires up order endpoints on the given router.
func (s *OrderService) RegisterRoutes(router *gin.Engine) {
	router.POST("/orders", s.createOrder)
	router.GET("/orders/:id", s.getOrder)
}

func (s *OrderService) createOrder(c *gin.Context) {
	ctx, span := orderTracer.Start(c.Request.Context(), "CreateOrder")
	defer span.End()
	start := time.Now()

	var req CreateOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.requestCount.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "bad_request")))
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	span.SetAttributes(
		attribute.Int("order.user_id", req.UserID),
		attribute.Int("order.product_id", req.ProductID),
		attribute.Int("order.quantity", req.Quantity),
	)

	// Look up product price.
	var price float64
	err := s.db.QueryRow(ctx,
		"SELECT price FROM products WHERE id = $1", req.ProductID,
	).Scan(&price)
	if err != nil {
		s.logger.Error("product lookup failed", zap.Error(err))
		s.requestCount.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "error")))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "product lookup failed"})
		return
	}

	total := price * float64(req.Quantity)

	// Insert order.
	var orderID int
	err = s.db.QueryRow(ctx,
		`INSERT INTO orders (user_id, product_id, quantity, total, status)
		 VALUES ($1, $2, $3, $4, 'pending') RETURNING id`,
		req.UserID, req.ProductID, req.Quantity, total,
	).Scan(&orderID)
	if err != nil {
		s.logger.Error("order insert failed", zap.Error(err))
		s.requestCount.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "error")))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create order"})
		return
	}

	// Call payment service.
	paymentStatus := s.processPayment(ctx, orderID, total)

	// Update order status based on payment result.
	_, err = s.db.Exec(ctx,
		"UPDATE orders SET status = $1 WHERE id = $2", paymentStatus, orderID,
	)
	if err != nil {
		s.logger.Error("order status update failed", zap.Error(err))
	}

	elapsed := float64(time.Since(start).Milliseconds())
	s.requestLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String("endpoint", "POST /orders")))
	s.requestCount.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "ok")))

	s.logger.Info("order created",
		zap.Int("order_id", orderID),
		zap.String("payment_status", paymentStatus),
	)

	c.JSON(http.StatusCreated, gin.H{
		"id":             orderID,
		"total":          total,
		"payment_status": paymentStatus,
	})
}

func (s *OrderService) processPayment(ctx context.Context, orderID int, amount float64) string {
	_, span := orderTracer.Start(ctx, "ProcessPaymentCall")
	defer span.End()

	payload, _ := json.Marshal(map[string]interface{}{
		"order_id": orderID,
		"amount":   amount,
	})

	resp, err := http.Post(s.paymentURL+"/payments/process", "application/json", bytes.NewReader(payload))
	if err != nil {
		s.logger.Error("payment call failed", zap.Error(err))
		span.RecordError(err)
		return "payment_failed"
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "payment_failed"
	}
	return "confirmed"
}

func (s *OrderService) getOrder(c *gin.Context) {
	ctx, span := orderTracer.Start(c.Request.Context(), "GetOrder")
	defer span.End()
	start := time.Now()

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid order id"})
		return
	}
	span.SetAttributes(attribute.Int("order.id", id))

	var order Order
	err = s.db.QueryRow(ctx,
		"SELECT id, user_id, product_id, quantity, total, status FROM orders WHERE id = $1", id,
	).Scan(&order.ID, &order.UserID, &order.ProductID, &order.Quantity, &order.Total, &order.Status)
	if err != nil {
		s.logger.Error("order not found", zap.Int("id", id), zap.Error(err))
		c.JSON(http.StatusNotFound, gin.H{"error": "order not found"})
		return
	}

	elapsed := float64(time.Since(start).Milliseconds())
	s.requestLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String("endpoint", "GET /orders/:id")))
	s.requestCount.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "ok")))

	c.JSON(http.StatusOK, order)
}
