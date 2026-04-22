package services

import (
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

var userTracer = otel.Tracer("shopsim/user")

// UserService handles user-related HTTP operations.
type UserService struct {
	db             *pgxpool.Pool
	logger         *zap.Logger
	requestCount   metric.Int64Counter
	requestLatency metric.Float64Histogram
}

// User represents a user entity.
type User struct {
	ID    int    `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// CreateUserRequest is the payload for POST /users.
type CreateUserRequest struct {
	Email string `json:"email" binding:"required,email"`
	Name  string `json:"name" binding:"required"`
}

// NewUserService creates a new UserService with OTel metrics initialised.
func NewUserService(db *pgxpool.Pool, logger *zap.Logger) (*UserService, error) {
	meter := otel.Meter("shopsim/user")

	reqCount, err := meter.Int64Counter("user.request.count",
		metric.WithDescription("Total number of user requests"),
	)
	if err != nil {
		return nil, fmt.Errorf("creating request count metric: %w", err)
	}

	reqLatency, err := meter.Float64Histogram("user.request.latency_ms",
		metric.WithDescription("User request latency in milliseconds"),
	)
	if err != nil {
		return nil, fmt.Errorf("creating request latency metric: %w", err)
	}

	return &UserService{
		db:             db,
		logger:         logger,
		requestCount:   reqCount,
		requestLatency: reqLatency,
	}, nil
}

// RegisterRoutes wires up user endpoints on the given router.
func (s *UserService) RegisterRoutes(router *gin.Engine) {
	router.GET("/users/:id", s.getUser)
	router.POST("/users", s.createUser)
}

func (s *UserService) getUser(c *gin.Context) {
	ctx, span := userTracer.Start(c.Request.Context(), "GetUser")
	defer span.End()
	start := time.Now()

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}
	span.SetAttributes(attribute.Int("user.id", id))

	var user User
	err = s.db.QueryRow(ctx,
		"SELECT id, email, name FROM users WHERE id = $1", id,
	).Scan(&user.ID, &user.Email, &user.Name)
	if err != nil {
		s.logger.Error("user not found", zap.Int("id", id), zap.Error(err))
		s.requestCount.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "not_found")))
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	elapsed := float64(time.Since(start).Milliseconds())
	s.requestLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String("endpoint", "GET /users/:id")))
	s.requestCount.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "ok")))

	c.JSON(http.StatusOK, user)
}

func (s *UserService) createUser(c *gin.Context) {
	ctx, span := userTracer.Start(c.Request.Context(), "CreateUser")
	defer span.End()
	start := time.Now()

	var req CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.requestCount.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "bad_request")))
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var id int
	err := s.db.QueryRow(ctx,
		"INSERT INTO users (email, name) VALUES ($1, $2) RETURNING id",
		req.Email, req.Name,
	).Scan(&id)
	if err != nil {
		s.logger.Error("user insert failed", zap.Error(err))
		s.requestCount.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "error")))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create user"})
		return
	}

	elapsed := float64(time.Since(start).Milliseconds())
	s.requestLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String("endpoint", "POST /users")))
	s.requestCount.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "ok")))

	s.logger.Info("user created", zap.Int("user_id", id))
	c.JSON(http.StatusCreated, gin.H{"id": id})
}
