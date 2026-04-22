package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

var productTracer = otel.Tracer("shopsim/product")

// ProductService handles product-related HTTP operations.
type ProductService struct {
	db             *pgxpool.Pool
	rdb            *redis.Client
	logger         *zap.Logger
	requestCount   metric.Int64Counter
	requestLatency metric.Float64Histogram
}

// Product represents a product entity.
type Product struct {
	ID          int     `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Price       float64 `json:"price"`
	Stock       int     `json:"stock"`
}

// CreateProductRequest is the payload for POST /products.
type CreateProductRequest struct {
	Name        string  `json:"name" binding:"required"`
	Description string  `json:"description"`
	Price       float64 `json:"price" binding:"required"`
	Stock       int     `json:"stock"`
}

// NewProductService creates a new ProductService with OTel metrics initialised.
func NewProductService(db *pgxpool.Pool, rdb *redis.Client, logger *zap.Logger) (*ProductService, error) {
	meter := otel.Meter("shopsim/product")

	reqCount, err := meter.Int64Counter("product.request.count",
		metric.WithDescription("Total number of product requests"),
	)
	if err != nil {
		return nil, fmt.Errorf("creating request count metric: %w", err)
	}

	reqLatency, err := meter.Float64Histogram("product.request.latency_ms",
		metric.WithDescription("Product request latency in milliseconds"),
	)
	if err != nil {
		return nil, fmt.Errorf("creating request latency metric: %w", err)
	}

	return &ProductService{
		db:             db,
		rdb:            rdb,
		logger:         logger,
		requestCount:   reqCount,
		requestLatency: reqLatency,
	}, nil
}

// RegisterRoutes wires up product endpoints on the given router.
func (s *ProductService) RegisterRoutes(router *gin.Engine) {
	router.GET("/products", s.listProducts)
	router.GET("/products/:id", s.getProduct)
	router.POST("/products", s.createProduct)
}

func (s *ProductService) listProducts(c *gin.Context) {
	ctx, span := productTracer.Start(c.Request.Context(), "ListProducts")
	defer span.End()
	start := time.Now()

	rows, err := s.db.Query(ctx,
		"SELECT id, name, description, price, stock FROM products ORDER BY id LIMIT 100",
	)
	if err != nil {
		s.logger.Error("failed to list products", zap.Error(err))
		s.requestCount.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "error")))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list products"})
		return
	}
	defer rows.Close()

	var products []Product
	for rows.Next() {
		var p Product
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.Price, &p.Stock); err != nil {
			s.logger.Error("scan error", zap.Error(err))
			continue
		}
		products = append(products, p)
	}

	elapsed := float64(time.Since(start).Milliseconds())
	s.requestLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String("endpoint", "GET /products")))
	s.requestCount.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "ok")))

	c.JSON(http.StatusOK, products)
}

func (s *ProductService) getProduct(c *gin.Context) {
	ctx, span := productTracer.Start(c.Request.Context(), "GetProduct")
	defer span.End()
	start := time.Now()

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid product id"})
		return
	}
	span.SetAttributes(attribute.Int("product.id", id))

	// Try Redis cache first.
	product, err := s.getFromCache(ctx, id)
	if err == nil {
		s.logger.Debug("cache hit", zap.Int("product_id", id))
		elapsed := float64(time.Since(start).Milliseconds())
		s.requestLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String("endpoint", "GET /products/:id")))
		s.requestCount.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "ok"), attribute.Bool("cache_hit", true)))
		c.JSON(http.StatusOK, product)
		return
	}

	// Cache miss — query PostgreSQL.
	s.logger.Debug("cache miss, querying postgres", zap.Int("product_id", id))
	product = &Product{}
	err = s.db.QueryRow(ctx,
		"SELECT id, name, description, price, stock FROM products WHERE id = $1", id,
	).Scan(&product.ID, &product.Name, &product.Description, &product.Price, &product.Stock)
	if err != nil {
		s.logger.Error("product not found", zap.Int("id", id), zap.Error(err))
		c.JSON(http.StatusNotFound, gin.H{"error": "product not found"})
		return
	}

	// Populate cache (best-effort).
	if cacheErr := s.setCache(ctx, id, product); cacheErr != nil {
		s.logger.Warn("failed to set cache", zap.Error(cacheErr))
	}

	elapsed := float64(time.Since(start).Milliseconds())
	s.requestLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String("endpoint", "GET /products/:id")))
	s.requestCount.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "ok"), attribute.Bool("cache_hit", false)))

	c.JSON(http.StatusOK, product)
}

func (s *ProductService) createProduct(c *gin.Context) {
	ctx, span := productTracer.Start(c.Request.Context(), "CreateProduct")
	defer span.End()
	start := time.Now()

	var req CreateProductRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.requestCount.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "bad_request")))
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var id int
	err := s.db.QueryRow(ctx,
		`INSERT INTO products (name, description, price, stock)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		req.Name, req.Description, req.Price, req.Stock,
	).Scan(&id)
	if err != nil {
		s.logger.Error("product insert failed", zap.Error(err))
		s.requestCount.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "error")))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create product"})
		return
	}

	elapsed := float64(time.Since(start).Milliseconds())
	s.requestLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String("endpoint", "POST /products")))
	s.requestCount.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "ok")))

	s.logger.Info("product created", zap.Int("product_id", id))
	c.JSON(http.StatusCreated, gin.H{"id": id})
}

// --- Redis cache helpers ---

func cacheKey(id int) string {
	return fmt.Sprintf("product:%d", id)
}

func (s *ProductService) getFromCache(ctx context.Context, id int) (*Product, error) {
	_, span := productTracer.Start(ctx, "RedisGet")
	defer span.End()

	data, err := s.rdb.Get(ctx, cacheKey(id)).Bytes()
	if err != nil {
		return nil, err
	}
	var p Product
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *ProductService) setCache(ctx context.Context, id int, p *Product) error {
	_, span := productTracer.Start(ctx, "RedisSet")
	defer span.End()

	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, cacheKey(id), data, 5*time.Minute).Err()
}
