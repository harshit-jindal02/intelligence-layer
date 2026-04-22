# PredictAssist — Architecture & Build Roadmap

## Vision

```
┌─────────────┐       ┌──────────────┐       ┌─────────────┐
│ TraceAssist  │──────▶│ PredictAssist │──────▶│  toilAssist  │
│ (Data Layer) │ OTLP  │ (Intelligence)│ Events│(Action Layer)│
│              │       │              │       │              │
│ • Metrics    │       │ • Detect     │       │ • Jira tickets│
│ • Logs       │       │ • Predict    │       │ • LLM remediation│
│ • Traces     │       │ • Reason     │       │ • Runbooks   │
└─────────────┘       └──────────────┘       └─────────────┘
```

**Today:** PredictAssist is a solo product. It ingests data directly from a sample app via OpenTelemetry (OTLP).

**Tomorrow:** TraceAssist replaces the direct OTLP receiver — PredictAssist subscribes to TraceAssist's data stream. toilAssist subscribes to PredictAssist's prediction events.

**The integration contract:** All three systems communicate via two protocols:
1. **OTLP (OpenTelemetry Protocol)** — for telemetry data (logs, metrics, traces)
2. **Event Bus (NATS)** — for prediction events, action triggers, and status updates

This means PredictAssist doesn't need to know *who* sends the data — only that it arrives in OTLP format. Same for toilAssist — it only cares about prediction events on the bus.

---

## System Architecture

```
                    ┌─────────────────────────────────────────────────┐
                    │              PredictAssist                       │
                    │                                                 │
  OTLP/gRPC        │  ┌──────────┐    ┌──────────┐    ┌──────────┐  │   NATS
  (port 4317) ─────┼─▶│ Ingestion │───▶│ Feature  │───▶│ Anomaly  │──┼──────▶ prediction.detected
                    │  │ Service   │    │ Engine   │    │ Detector │  │
                    │  └──────────┘    └──────────┘    └──────────┘  │
                    │       │                               │         │
                    │       ▼                               ▼         │
                    │  ┌──────────┐                  ┌──────────┐    │
                    │  │ ClickHouse│                  │Correlator│    │   NATS
                    │  │ (Storage) │◀─────────────────│ Engine   │────┼──────▶ prediction.incident
                    │  └──────────┘                   └──────────┘   │
                    │       │                               │         │
                    │       ▼                               ▼         │
                    │  ┌──────────┐                  ┌──────────┐    │   NATS
                    │  │  Redis   │                  │   LLM    │────┼──────▶ prediction.insight
                    │  │ (Cache)  │                  │ Reasoner │    │
                    │  └──────────┘                  └──────────┘    │
                    │                                                 │
                    │  ┌──────────────────────────────────────┐      │
                    │  │          API / gRPC Gateway           │      │
                    │  │  • Query anomalies                    │      │
                    │  │  • Manage incident fingerprints       │      │
                    │  │  • View predictions & insights        │      │
                    │  │  • Health / readiness probes           │      │
                    │  └──────────────────────────────────────┘      │
                    └─────────────────────────────────────────────────┘
```

---

## Component Breakdown

### 1. Ingestion Service

**Responsibility:** Receive OTLP data, normalize it, and write to storage + feature pipeline.

```
Package: internal/ingestion

Key interfaces:
  - OTLPReceiver     → receives logs, metrics, traces via gRPC (port 4317)
  - Normalizer       → converts OTLP data into internal canonical models
  - Writer           → batched writes to ClickHouse + publishes to feature engine

Why OTLP:
  - Standard protocol — TraceAssist will speak it natively later
  - OTel Collector can sit in front for routing/filtering if needed
  - Sample app uses OTel SDK directly, so zero custom protocol work
```

**Go libraries:**
- `go.opentelemetry.io/collector/receiver/otlpreceiver` — or build a lightweight gRPC server using the OTLP protobuf definitions directly for more control
- `github.com/ClickHouse/clickhouse-go/v2` — ClickHouse client

### 2. Feature Engine

**Responsibility:** Compute behavioral features from raw telemetry in sliding time windows.

```
Package: internal/features

Key concepts:
  - FeatureDefinition   → declares a feature (name, source signal, window, computation)
  - FeatureComputer     → runs computations on incoming data
  - FeatureStore        → writes computed features to ClickHouse + Redis cache

Feature categories:
  ┌─────────────────────────────────────────────────────────────┐
  │ Rate features        │ error_rate_5m, request_rate_5m,      │
  │                      │ log_volume_velocity                   │
  ├──────────────────────┼──────────────────────────────────────┤
  │ Distribution features│ latency_p50, latency_p95, p99,       │
  │                      │ latency_stddev                        │
  ├──────────────────────┼──────────────────────────────────────┤
  │ Ratio features       │ error_to_success_ratio,               │
  │                      │ warn_to_info_ratio,                   │
  │                      │ 5xx_to_total_ratio                    │
  ├──────────────────────┼──────────────────────────────────────┤
  │ Delta features       │ cpu_usage_delta_15m,                  │
  │                      │ memory_growth_rate,                   │
  │                      │ goroutine_count_delta                 │
  ├──────────────────────┼──────────────────────────────────────┤
  │ Pattern features     │ unique_error_types_10m,               │
  │                      │ new_error_signature_detected,         │
  │                      │ log_entropy_shift                     │
  └──────────────────────┴──────────────────────────────────────┘

Window sizes: 1m, 5m, 15m, 1h (configurable per feature)
```

**Why this matters:** Raw metrics like "CPU is 78%" are nearly useless for prediction. But "CPU has increased 23% over the last 15 minutes while request rate is flat" is a signal. The feature engine bridges that gap.

### 3. Anomaly Detector

**Responsibility:** Detect anomalies in computed features using statistical methods.

```
Package: internal/detector

Algorithms (start simple, add complexity as needed):
  - ZScoreDetector       → flags features > N standard deviations from rolling mean
  - IQRDetector          → interquartile range for outlier detection (robust to skew)
  - ForecastDetector     → exponential smoothing forecast, flag when actual deviates
  - MultiSignalDetector  → correlates anomalies across related features

Output: AnomalyEvent {
    ID            string
    Timestamp     time.Time
    ServiceName   string
    Features      []AnomalyFeature  // which features triggered
    Severity      float64           // 0.0 - 1.0, based on deviation magnitude
    Correlated    []string          // IDs of co-occurring anomalies
}
```

**Go libraries:**
- `gonum.org/v1/gonum/stat` — statistical functions
- `gonum.org/v1/gonum/floats` — numerical operations
- No external ML framework needed — these are all implementable in pure Go

### 4. Correlator Engine

**Responsibility:** Match detected anomaly patterns against known incident fingerprints.

```
Package: internal/correlator

Key concepts:
  - IncidentFingerprint → stored pattern: sequence of anomaly types + timing that preceded
                           a known incident
  - PatternMatcher      → compares current anomaly stream against fingerprint library
  - ConfidenceScorer    → scores how closely current pattern matches known fingerprints

Data model:
  IncidentFingerprint {
      ID              string
      Name            string          // "DB connection pool exhaustion"
      Description     string
      AnomalySequence []AnomalyRule   // ordered list of expected anomalies
      TimeWindow      time.Duration   // how far back to look
      MinConfidence   float64         // threshold to trigger
      Severity        string          // critical, warning, info
      Remediation     string          // what to do (for LLM context)
  }

  AnomalyRule {
      FeatureName     string          // "error_rate_5m"
      ServicePattern  string          // glob: "api-*" or exact: "order-service"
      Condition       string          // "rising", "spike", "above_baseline"
      Required        bool            // must be present vs. nice-to-have
  }
```

**How fingerprints are created:**
1. **Manual:** You define them based on past incidents (start here)
2. **Semi-automatic:** After an incident, the system shows you what anomalies preceded it and asks "save as fingerprint?"
3. **Automatic (Phase 4+):** The system mines recurring anomaly patterns that precede degradations

### 5. LLM Reasoner

**Responsibility:** Provide natural-language interpretation of anomalies and predictions.

```
Package: internal/reasoner

Input: AnomalyEvent or IncidentPrediction + surrounding log context
Output: Insight {
    Summary       string   // "Order service is showing early signs of DB connection
                           //  pool exhaustion, similar to incident on March 15th"
    Evidence      []string // key log lines and metric values
    Confidence    float64
    Remediation   []string // suggested actions
    RelatedDocs   []string // links to runbooks, past incident reports
}

Implementation:
  - Build prompts from anomaly context + log snippets + incident history
  - Use RAG over past incident reports / runbooks for remediation suggestions
  - Call LLM via API (Claude API, or local model via Ollama for cost control)
  - Cache similar queries to reduce LLM calls
```

### 6. Storage Layer

```
ClickHouse (primary — analytical queries):
  Tables:
    raw_logs          — ingested logs (partitioned by day, service)
    raw_metrics       — ingested metrics (partitioned by day, metric name)
    computed_features — feature engine output (partitioned by day, service)
    anomaly_events    — detected anomalies
    incident_fingerprints — pattern library
    incident_history  — past incidents with linked anomalies

Redis (hot cache):
    feature:{service}:{feature_name}     — latest computed feature values
    baseline:{service}:{feature_name}    — rolling baseline stats
    anomaly:active:{service}             — currently active anomalies

Why ClickHouse:
  - Columnar storage → fast aggregation over time-series data
  - Excellent compression for logs/metrics
  - Native time-series functions (moving averages, quantiles)
  - Handles billions of rows on modest hardware
  - Go client is mature
```

### 7. Event Bus (NATS)

```
Subjects:
  telemetry.logs.{service}        — raw log events (internal)
  telemetry.metrics.{service}     — raw metric events (internal)
  features.computed.{service}     — feature computation results
  prediction.anomaly.detected     — anomaly detected
  prediction.incident.predicted   — incident pattern matched
  prediction.insight.generated    — LLM insight ready

  # Future: toilAssist subscribes to these
  action.requested                — request for automated action
  action.completed                — action result

Why NATS:
  - Written in Go, feels native
  - JetStream for persistence and replay
  - Lightweight — no JVM, no Zookeeper
  - Subject-based routing maps cleanly to the event taxonomy
  - Easy to add new consumers (toilAssist) without changing producers
```

### 8. API Gateway

```
Package: internal/api

gRPC + REST (via grpc-gateway) serving:
  /api/v1/anomalies          — list/query detected anomalies
  /api/v1/predictions        — active incident predictions
  /api/v1/insights           — LLM-generated insights
  /api/v1/fingerprints       — CRUD for incident fingerprints
  /api/v1/features           — query computed features
  /api/v1/health             — health check

  gRPC services:
    PredictionService        — streaming anomaly/prediction updates
    FingerprintService       — manage incident fingerprints
    QueryService             — historical queries

Why gRPC primary:
  - TraceAssist and toilAssist will use gRPC for inter-service communication
  - Protobuf contracts enforce schema across all three products
  - grpc-gateway gives you REST for free (dashboard, debugging)
```

---

## Sample Application: "ShopSim"

A small Go microservices application that simulates a realistic e-commerce system with built-in failure modes you can trigger to test PredictAssist.

```
                         ┌──────────────┐
                         │   Load Gen   │
                         │  (simulates  │
                         │   traffic)   │
                         └──────┬───────┘
                                │
                         ┌──────▼───────┐
                         │  API Gateway  │ :8080
                         └──────┬───────┘
                    ┌───────────┼───────────┐
             ┌──────▼──────┐ ┌─▼────────┐ ┌▼──────────┐
             │Order Service│ │ Product   │ │  User     │
             │   :8081     │ │ Service   │ │ Service   │
             └──────┬──────┘ │  :8082    │ │  :8083    │
                    │        └─┬────────┘ └┬──────────┘
             ┌──────▼──────┐   │           │
             │Payment Svc  │   │           │
             │   :8084     │   │           │
             └──────┬──────┘   │           │
                    │          │           │
             ┌──────▼──────────▼───────────▼──┐
             │         PostgreSQL              │
             │         + Redis Cache           │
             └────────────────────────────────┘

All services instrumented with OpenTelemetry → export to PredictAssist (OTLP :4317)
```

### Built-in Failure Modes (Chaos Endpoints)

Each service exposes `/chaos/*` endpoints to simulate realistic failures:

```
POST /chaos/db-slow          → Adds artificial latency to DB queries (gradual, like real degradation)
POST /chaos/memory-leak      → Starts allocating memory that isn't freed (goroutine leak sim)
POST /chaos/connection-pool  → Gradually exhausts DB connection pool
POST /chaos/cascade          → Injects errors in payment-service that cascade upstream
POST /chaos/cpu-spike        → Spins up goroutines doing busy work
POST /chaos/intermittent     → Random 500s at configurable rate (starts low, increases)
POST /chaos/log-flood        → Suddenly increases log volume (common pre-incident signal)
POST /chaos/dependency-slow  → Simulates a slow downstream dependency
POST /chaos/disk-pressure    → Simulates growing disk usage
POST /chaos/reset            → Clears all active chaos injections
```

**Why these specific failures:** Each one produces a distinct "fingerprint" in the telemetry data. Memory leaks show up as gradual metric drift. Cascade failures show correlated error spikes across services. DB connection pool exhaustion shows latency climbing before errors appear. These are the patterns PredictAssist needs to learn.

### Load Generator

```
Package: cmd/loadgen

Simulates realistic traffic patterns:
  - Base load: steady requests across all services
  - Diurnal pattern: higher traffic during "business hours" (compressed to minutes for testing)
  - Burst patterns: periodic spikes
  - User journeys: multi-step flows (browse → add to cart → checkout)

This is critical — without realistic baseline traffic, anomaly detection has nothing to compare against.
```

---

## Project Structure

```
predictassist/
├── cmd/
│   ├── predictassist/           # Main PredictAssist binary
│   │   └── main.go
│   ├── shopsim/                 # Sample app (all services in one binary, flag-selectable)
│   │   └── main.go
│   └── loadgen/                 # Traffic generator
│       └── main.go
│
├── internal/
│   ├── ingestion/
│   │   ├── otlp_receiver.go     # OTLP gRPC server
│   │   ├── normalizer.go        # Convert OTLP → internal models
│   │   └── writer.go            # Batch write to ClickHouse
│   │
│   ├── features/
│   │   ├── engine.go            # Feature computation coordinator
│   │   ├── definitions.go       # Feature definitions registry
│   │   ├── windows.go           # Sliding window implementation
│   │   ├── computers/
│   │   │   ├── rate.go          # Rate-based features
│   │   │   ├── distribution.go  # Percentile/distribution features
│   │   │   ├── ratio.go         # Ratio features
│   │   │   ├── delta.go         # Change-over-time features
│   │   │   └── pattern.go       # Log pattern features
│   │   └── store.go             # Feature storage (ClickHouse + Redis)
│   │
│   ├── detector/
│   │   ├── engine.go            # Detection coordinator
│   │   ├── zscore.go            # Z-score detector
│   │   ├── iqr.go               # IQR detector
│   │   ├── forecast.go          # Exponential smoothing forecast
│   │   ├── multisignal.go       # Cross-signal correlation
│   │   └── baseline.go          # Baseline computation and storage
│   │
│   ├── correlator/
│   │   ├── engine.go            # Pattern matching coordinator
│   │   ├── fingerprint.go       # Fingerprint data model and storage
│   │   ├── matcher.go           # Sequence matching algorithm
│   │   └── scorer.go            # Confidence scoring
│   │
│   ├── reasoner/
│   │   ├── engine.go            # LLM reasoning coordinator
│   │   ├── prompt.go            # Prompt construction
│   │   ├── rag.go               # RAG over incident history
│   │   └── cache.go             # Response caching
│   │
│   ├── store/
│   │   ├── clickhouse.go        # ClickHouse client and schema
│   │   ├── redis.go             # Redis client
│   │   └── migrations/          # ClickHouse schema migrations
│   │       ├── 001_raw_logs.sql
│   │       ├── 002_raw_metrics.sql
│   │       ├── 003_computed_features.sql
│   │       ├── 004_anomaly_events.sql
│   │       └── 005_incident_fingerprints.sql
│   │
│   ├── api/
│   │   ├── server.go            # gRPC + REST gateway
│   │   └── handlers/
│   │       ├── anomalies.go
│   │       ├── predictions.go
│   │       ├── fingerprints.go
│   │       └── features.go
│   │
│   └── events/
│       ├── bus.go               # NATS client wrapper
│       ├── subjects.go          # Subject constants
│       └── types.go             # Event type definitions
│
├── pkg/
│   ├── models/                  # Shared data models (used across all 3 products later)
│   │   ├── telemetry.go         # Log, Metric, Trace models
│   │   ├── anomaly.go           # AnomalyEvent model
│   │   ├── prediction.go        # Prediction model
│   │   └── insight.go           # Insight model
│   └── proto/                   # Protobuf definitions
│       ├── prediction.proto     # PredictAssist API
│       └── events.proto         # Event bus message types
│
├── shopsim/                     # Sample application code
│   ├── gateway/
│   │   └── gateway.go
│   ├── services/
│   │   ├── order.go
│   │   ├── product.go
│   │   ├── user.go
│   │   └── payment.go
│   ├── chaos/
│   │   ├── injector.go          # Chaos injection framework
│   │   ├── db_slow.go
│   │   ├── memory_leak.go
│   │   ├── connection_pool.go
│   │   ├── cascade.go
│   │   └── handlers.go          # HTTP handlers for chaos endpoints
│   ├── telemetry/
│   │   └── otel.go              # OpenTelemetry setup for all services
│   └── db/
│       ├── postgres.go
│       └── migrations/
│
├── deployments/
│   ├── docker-compose.yml       # Full stack: PredictAssist + ShopSim + infra
│   ├── docker-compose.dev.yml   # Dev overrides (hot reload, debug ports)
│   ├── Dockerfile.predictassist
│   ├── Dockerfile.shopsim
│   └── clickhouse/
│       └── config.xml           # ClickHouse configuration
│
├── configs/
│   ├── predictassist.yaml       # Main configuration
│   ├── features.yaml            # Feature definitions
│   └── fingerprints.yaml        # Initial incident fingerprints
│
├── scripts/
│   ├── seed-fingerprints.go     # Load initial fingerprints
│   └── simulate-incident.sh     # Run a scripted incident scenario
│
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

---

## Integration Design (Future-Proofing)

### How TraceAssist Will Connect Later

```
Today (solo mode):
  ShopSim → [OTel SDK] → OTLP :4317 → PredictAssist Ingestion

Future (integrated mode):
  Any App → [OTel SDK] → TraceAssist → [OTLP forward / NATS] → PredictAssist Ingestion
```

**What to build now:** The ingestion service accepts standard OTLP. That's the contract. When TraceAssist exists, it either:
- Forwards OTLP to PredictAssist (simplest — PredictAssist doesn't change at all)
- Publishes to NATS, and PredictAssist adds a NATS consumer alongside the OTLP receiver

**Design rule:** The `internal/ingestion` package accepts an `io.TelemetrySource` interface. Today, the only implementation is `OTLPReceiver`. Later, add `NATSConsumer` or `TraceAssistClient` without touching any downstream code.

```go
// internal/ingestion/source.go
type TelemetrySource interface {
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    // Channels for streaming data to the feature engine
    Logs() <-chan *models.LogRecord
    Metrics() <-chan *models.MetricRecord
    Traces() <-chan *models.TraceRecord
}
```

### How toilAssist Will Connect Later

```
Today (solo mode):
  PredictAssist → publishes prediction events to NATS → (nobody listens yet, but events are persisted in JetStream)

Future (integrated mode):
  PredictAssist → NATS "prediction.incident.predicted" → toilAssist subscribes
  toilAssist → creates Jira ticket → triggers LLM remediation → publishes "action.completed"
  PredictAssist → subscribes to "action.completed" → updates incident status
```

**What to build now:** PredictAssist publishes well-structured events to NATS with all the context toilAssist will need:

```go
// pkg/models/prediction.go
type IncidentPrediction struct {
    ID              string            `json:"id"`
    Timestamp       time.Time         `json:"timestamp"`
    FingerprintID   string            `json:"fingerprint_id"`
    FingerprintName string            `json:"fingerprint_name"`
    Confidence      float64           `json:"confidence"`
    Severity        string            `json:"severity"`
    AffectedServices []string         `json:"affected_services"`
    Evidence        []AnomalyEvidence `json:"evidence"`
    Insight         *Insight          `json:"insight,omitempty"`
    SuggestedActions []string         `json:"suggested_actions"`
    // toilAssist will use these fields to create Jira tickets
    // and feed them to its LLM remediation engine
}
```

---

## Build Roadmap — What to Build, In What Order

### Sprint 1 (Week 1-2): Foundation + Sample App

**Goal: Get data flowing end-to-end.**

```
Build:
  1. ShopSim — API gateway + order service + product service (minimal, just enough to
     generate traffic). Instrument with OpenTelemetry.
  2. Load generator — steady traffic to ShopSim.
  3. docker-compose — ShopSim + PostgreSQL + ClickHouse + Redis + NATS
  4. Ingestion service — OTLP receiver that writes raw logs/metrics to ClickHouse.

Test:
  - Start the stack, run load generator, verify data appears in ClickHouse.
  - Run: SELECT count() FROM raw_logs WHERE service = 'order-service'

You now have telemetry flowing into storage. This is your data foundation.
```

### Sprint 2 (Week 3-4): Feature Engine + Basic Detection

**Goal: Compute features and detect simple anomalies.**

```
Build:
  1. Feature engine — compute error_rate_5m, latency_p95, request_rate_5m
     for each service. Write to ClickHouse + cache in Redis.
  2. Z-score detector — flag features that deviate > 3 sigma from rolling baseline.
  3. First chaos endpoint — POST /chaos/intermittent on order-service
     (inject random 500 errors).

Test:
  - Run load generator for 30 minutes to build baseline.
  - Trigger chaos. Verify anomaly is detected within 5 minutes.
  - Check: anomaly_events table has entries for order-service error_rate spike.
```

### Sprint 3 (Week 5-6): More Features + Multi-Signal Correlation

**Goal: Detect complex, multi-signal anomaly patterns.**

```
Build:
  1. Remaining feature computers — ratios, deltas, distribution features.
  2. More chaos endpoints — db-slow, memory-leak, connection-pool, cascade.
  3. Multi-signal detector — correlate anomalies across features and services.
  4. NATS event publishing — anomaly events published to prediction.anomaly.detected.

Test:
  - Trigger cascade chaos. Verify system detects correlated anomalies across
    order-service AND payment-service (not just individual spikes).
  - Trigger memory-leak. Verify system detects gradual drift (not just spikes).
```

### Sprint 4 (Week 7-8): Incident Fingerprinting

**Goal: Predict known incident types before they fully manifest.**

```
Build:
  1. Fingerprint data model and storage.
  2. Seed 3-5 fingerprints based on your chaos scenarios:
     - "DB connection pool exhaustion" (latency rises → error rate rises → timeouts)
     - "Memory leak" (memory growth + goroutine count growth over 15m)
     - "Cascade failure" (error spike in downstream → error spike in upstream)
  3. Pattern matcher — compare active anomalies against fingerprint library.
  4. Confidence scorer.
  5. API endpoints for querying predictions and managing fingerprints.

Test:
  - Trigger connection-pool chaos. Verify system predicts "DB connection pool
    exhaustion" BEFORE errors actually spike (during the latency-rising phase).
  - This is the key moment — you're predicting, not just detecting.
```

### Sprint 5 (Week 9-10): LLM Reasoning Layer

**Goal: Turn anomaly data into actionable intelligence.**

```
Build:
  1. Prompt construction — build context from anomaly events + surrounding logs.
  2. LLM integration — Claude API or local model via Ollama.
  3. Insight generation — natural language explanation + remediation suggestions.
  4. RAG foundation — index past incidents for retrieval.

Test:
  - Trigger an incident scenario. Verify the system produces a coherent
    explanation like: "Order service latency has increased 340% over the
    last 12 minutes. This pattern matches the DB connection pool exhaustion
    incident from [date]. The connection pool is likely approaching capacity.
    Suggested action: increase pool size or investigate long-running queries."
```

### Sprint 6 (Week 11-12): Polish + Integration Prep

**Goal: Production-ready solo product, ready for future integration.**

```
Build:
  1. API gateway — full gRPC + REST API.
  2. Dashboard (optional) — simple web UI showing anomalies, predictions, insights.
  3. Semi-automatic fingerprint creation — "save as fingerprint" flow after incidents.
  4. Integration interfaces — TelemetrySource interface, clean event contracts.
  5. Documentation — API docs, fingerprint authoring guide, deployment guide.

Test:
  - Run a full incident simulation end-to-end.
  - Verify: data ingested → features computed → anomaly detected → pattern matched
    → insight generated → event published to NATS.
```

---

## Key Go Dependencies

```go
// go.mod (core dependencies)

// OpenTelemetry
go.opentelemetry.io/proto/otlp          // OTLP protobuf definitions
go.opentelemetry.io/otel                 // OTel API (for sample app instrumentation)
go.opentelemetry.io/otel/sdk             // OTel SDK
go.opentelemetry.io/otel/exporters/otlp  // OTLP exporter (for sample app)

// Storage
github.com/ClickHouse/clickhouse-go/v2   // ClickHouse client
github.com/redis/go-redis/v9             // Redis client

// Event bus
github.com/nats-io/nats.go              // NATS client
github.com/nats-io/nats-server/v2       // Embedded NATS (for testing)

// Statistics
gonum.org/v1/gonum                       // Numerical/statistical computation

// API
google.golang.org/grpc                   // gRPC server
github.com/grpc-ecosystem/grpc-gateway/v2 // REST gateway
google.golang.org/protobuf               // Protobuf

// Infrastructure
github.com/spf13/viper                   // Configuration
go.uber.org/zap                          // Structured logging
github.com/jackc/pgx/v5                  // PostgreSQL (for sample app)
```

---

## Configuration Example

```yaml
# configs/predictassist.yaml

ingestion:
  otlp:
    grpc_port: 4317
    max_batch_size: 1000
    flush_interval: 5s

features:
  windows: [1m, 5m, 15m, 1h]
  computation_interval: 30s
  
detection:
  zscore:
    threshold: 3.0
    min_samples: 100       # don't flag until baseline has enough data
  baseline:
    window: 24h            # rolling baseline period
    update_interval: 5m

correlation:
  lookback_window: 30m     # how far back to look for related anomalies
  min_signals: 2           # minimum correlated anomalies to report

reasoner:
  provider: claude         # claude | ollama | openai
  model: claude-sonnet-4-5-20241022
  max_context_logs: 50     # max log lines to include in prompt
  cache_ttl: 15m

storage:
  clickhouse:
    addr: localhost:9000
    database: predictassist
  redis:
    addr: localhost:6379

events:
  nats:
    url: nats://localhost:4222
    stream: predictassist

api:
  grpc_port: 9090
  rest_port: 8090
```

---

## Docker Compose (Dev Stack)

```yaml
# deployments/docker-compose.yml
version: '3.8'

services:
  # --- Infrastructure ---
  clickhouse:
    image: clickhouse/clickhouse-server:24.3
    ports: ["9000:9000", "8123:8123"]
    volumes:
      - clickhouse-data:/var/lib/clickhouse
      - ./clickhouse/config.xml:/etc/clickhouse-server/config.d/custom.xml

  redis:
    image: redis:7-alpine
    ports: ["6379:6379"]

  nats:
    image: nats:2.10-alpine
    command: ["--jetstream", "--store_dir=/data"]
    ports: ["4222:4222", "8222:8222"]  # 8222 = monitoring
    volumes:
      - nats-data:/data

  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: shopsim
      POSTGRES_USER: shopsim
      POSTGRES_PASSWORD: shopsim
    ports: ["5432:5432"]

  # --- Sample Application ---
  shopsim-gateway:
    build: { context: ., dockerfile: deployments/Dockerfile.shopsim }
    command: ["--service=gateway", "--port=8080"]
    ports: ["8080:8080"]
    environment:
      OTEL_EXPORTER_OTLP_ENDPOINT: predictassist:4317
    depends_on: [postgres, redis, predictassist]

  shopsim-order:
    build: { context: ., dockerfile: deployments/Dockerfile.shopsim }
    command: ["--service=order", "--port=8081"]
    ports: ["8081:8081"]
    environment:
      OTEL_EXPORTER_OTLP_ENDPOINT: predictassist:4317
    depends_on: [postgres, redis, predictassist]

  shopsim-product:
    build: { context: ., dockerfile: deployments/Dockerfile.shopsim }
    command: ["--service=product", "--port=8082"]
    environment:
      OTEL_EXPORTER_OTLP_ENDPOINT: predictassist:4317
    depends_on: [postgres, redis, predictassist]

  shopsim-payment:
    build: { context: ., dockerfile: deployments/Dockerfile.shopsim }
    command: ["--service=payment", "--port=8084"]
    environment:
      OTEL_EXPORTER_OTLP_ENDPOINT: predictassist:4317
    depends_on: [postgres, redis, predictassist]

  loadgen:
    build: { context: ., dockerfile: deployments/Dockerfile.shopsim }
    command: ["--mode=loadgen", "--target=http://shopsim-gateway:8080", "--rps=50"]
    depends_on: [shopsim-gateway]

  # --- PredictAssist ---
  predictassist:
    build: { context: ., dockerfile: deployments/Dockerfile.predictassist }
    ports: ["4317:4317", "9090:9090", "8090:8090"]
    depends_on: [clickhouse, redis, nats]
    volumes:
      - ./configs:/etc/predictassist

volumes:
  clickhouse-data:
  nats-data:
```

---

## Where to Start (literally, today)

```bash
# 1. Create the project
mkdir predictassist && cd predictassist
go mod init github.com/yourusername/predictassist

# 2. Start with the sample app — you need data before you need intelligence
#    Build the simplest possible order-service with OTel instrumentation.
#    Get it writing logs and metrics to stdout first, then to OTLP.

# 3. Stand up infrastructure
#    docker compose up clickhouse redis nats postgres

# 4. Build ingestion service — receive OTLP, write to ClickHouse
#    This is your first real PredictAssist code.

# 5. Verify data flows end to end before building any intelligence.
```

**The single most important principle:** Get data flowing first. Intelligence without data is useless. Build ShopSim + Ingestion before touching the feature engine or detector. You can always add smarter analysis later, but you can't analyze what you haven't collected.
