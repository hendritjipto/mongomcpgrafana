// Gin HTTP interface backed by MongoDB. otelgin creates a root span per request;
// the background loop wraps its work in its own root span. Mongo operations get a
// context carrying a span (otelmongo), so they nest into the trace.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/x/mongo/driver/connstring"

	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/contrib/instrumentation/go.mongodb.org/mongo-driver/mongo/otelmongo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

func serviceName() string {
	if n := os.Getenv("OTEL_SERVICE_NAME"); n != "" {
		return n
	}
	return "go-app"
}

func initTracer(ctx context.Context) (*sdktrace.TracerProvider, error) {
	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithInsecure())
	if err != nil {
		return nil, err
	}
	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(semconv.ServiceName(serviceName())),
	)
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	return tp, nil
}

// explainFind runs an explain inside its own span and attaches plan attributes
// (the Go equivalent of the Node responseHook). Returns the raw plan for the API.
func explainFind(ctx context.Context, db *mongo.Database, coll string, filter bson.M) (bson.M, error) {
	ctx, span := otel.Tracer(serviceName()).Start(ctx, "mongodb.explain")
	defer span.End()

	cmd := bson.D{
		{Key: "explain", Value: bson.D{
			{Key: "find", Value: coll},
			{Key: "filter", Value: filter},
		}},
		{Key: "verbosity", Value: "executionStats"},
	}

	var res bson.M
	if err := db.RunCommand(ctx, cmd).Decode(&res); err != nil {
		span.RecordError(err)
		return nil, err
	}

	qp := asM(res["queryPlanner"])
	winning := asM(qp["winningPlan"])
	es := asM(res["executionStats"])

	stage, _ := winning["stage"].(string)
	if stage == "" {
		stage = "N/A"
	}
	shapeHash, _ := res["queryShapeHash"].(string)
	if shapeHash == "" {
		shapeHash = "N/A"
	}
	winningJSON, _ := json.Marshal(winning)

	span.SetAttributes(
		attribute.String("mongodb.queryShapeHash", shapeHash),
		attribute.String("mongodb.stage", stage),
		attribute.String("mongodb.winningPlan", string(winningJSON)),
		attribute.Int64("mongodb.totalDocsExamined", toInt64(es["totalDocsExamined"])),
		attribute.Int64("mongodb.totalKeysExamined", toInt64(es["totalKeysExamined"])),
		attribute.Int64("mongodb.nReturned", toInt64(es["nReturned"])),
	)
	return res, nil
}

func asM(v interface{}) bson.M {
	if m, ok := v.(bson.M); ok {
		return m
	}
	return bson.M{}
}

func toInt64(v interface{}) int64 {
	switch n := v.(type) {
	case int32:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}

func main() {
	ctx := context.Background()

	tp, err := initTracer(ctx)
	if err != nil {
		log.Fatalf("failed to init tracer: %v", err)
	}
	defer func() { _ = tp.Shutdown(ctx) }()

	uri := os.Getenv("MONGODB_URI")
	dbName := "grafana_pdc"
	if cs, err := connstring.ParseAndValidate(uri); err == nil && cs.Database != "" {
		dbName = cs.Database
	}

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri).SetMonitor(otelmongo.NewMonitor()))
	if err != nil {
		log.Fatalf("failed to connect to MongoDB: %v", err)
	}
	defer func() { _ = client.Disconnect(ctx) }()
	if err := client.Ping(ctx, nil); err != nil {
		log.Fatalf("failed to ping MongoDB: %v", err)
	}
	log.Println("✅ go-app connected to MongoDB")

	db := client.Database(dbName)
	coll := db.Collection("go_events")

	// Background loop — wrapped in a root span so the Mongo ops attach to a trace.
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			loopCtx, span := otel.Tracer(serviceName()).Start(context.Background(), "background-event-loop")
			if _, err := coll.InsertOne(loopCtx, bson.M{"source": "go-app", "ts": time.Now()}); err != nil {
				span.RecordError(err)
				log.Printf("go-app insert error: %v", err)
			} else if _, err := explainFind(loopCtx, db, "go_events", bson.M{"source": "go-app"}); err != nil {
				log.Printf("go-app explain error: %v", err)
			}
			span.End()
		}
	}()

	// HTTP interface (otelgin creates a root span per request; pass c.Request.Context() to Mongo).
	r := gin.New()
	r.Use(otelgin.Middleware(serviceName()))

	r.GET("/health", func(c *gin.Context) {
		if err := client.Ping(c.Request.Context(), nil); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	r.GET("/events", func(c *gin.Context) {
		reqCtx := c.Request.Context()
		opts := options.Find().SetSort(bson.D{{Key: "ts", Value: -1}}).SetLimit(10)
		cur, err := coll.Find(reqCtx, bson.M{}, opts)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		var recent []bson.M
		if err := cur.All(reqCtx, &recent); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		count, _ := coll.CountDocuments(reqCtx, bson.M{})
		c.JSON(http.StatusOK, gin.H{"count": count, "recent": recent})
	})

	r.POST("/events", func(c *gin.Context) {
		res, err := coll.InsertOne(c.Request.Context(), bson.M{"source": "go-app", "ts": time.Now()})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, gin.H{"insertedId": res.InsertedID})
	})

	r.GET("/explain", func(c *gin.Context) {
		plan, err := explainFind(c.Request.Context(), db, "go_events", bson.M{"source": "go-app"})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, plan)
	})

	log.Println("🚀 go-app listening on :8080")
	if err := r.Run(":8080"); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
