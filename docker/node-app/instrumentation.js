// instrumentation.js — preloaded via `node --import ./instrumentation.js index.js`
// Sets up OpenTelemetry with custom MongoDB instrumentation (mirrors sample/instrumentation.js).
import { NodeSDK } from "@opentelemetry/sdk-node";
import { MongoDBInstrumentation } from "@opentelemetry/instrumentation-mongodb";
import { getNodeAutoInstrumentations } from "@opentelemetry/auto-instrumentations-node";
import { OTLPLogExporter } from "@opentelemetry/exporter-logs-otlp-http";
import { BatchLogRecordProcessor } from "@opentelemetry/sdk-logs";
import { OTLPMetricExporter } from "@opentelemetry/exporter-metrics-otlp-http";
import { PeriodicExportingMetricReader } from "@opentelemetry/sdk-metrics";
import { RuntimeNodeInstrumentation } from "@opentelemetry/instrumentation-runtime-node";
import { HostMetrics } from "@opentelemetry/host-metrics";
import { BSON } from "bson";

const endpoint = process.env.OTEL_EXPORTER_OTLP_ENDPOINT;

const sdk = new NodeSDK({
  logRecordProcessor: new BatchLogRecordProcessor(
    new OTLPLogExporter({ url: endpoint + "/v1/logs" })
  ),
  metricReader: new PeriodicExportingMetricReader({
    exporter: new OTLPMetricExporter({ url: endpoint + "/v1/metrics" }),
    exportIntervalMillis: 5000,
  }),
  instrumentations: [
    getNodeAutoInstrumentations({
      "@opentelemetry/instrumentation-mongodb": { enabled: false },
    }),
    new RuntimeNodeInstrumentation({ monitoringPrecision: 5000 }),
    new MongoDBInstrumentation({
      enhancedDatabaseReporting: true,
      responseHook: (span, responseInfo) => {
        try {
          const parsedData = responseInfo.data;
          if (span.attributes["db.operation"] === "explain") {
            const document = BSON.deserialize(parsedData.bson);
            span.setAttributes({
              "mongodb.queryShapeHash": document.queryShapeHash || "N/A",
              "mongodb.stage": document.queryPlanner.winningPlan.stage || "N/A",
              "mongodb.winningPlan":
                JSON.stringify(document.queryPlanner.winningPlan, null, 2) || "N/A",
              "mongodb.totalDocsExamined": document.executionStats.totalDocsExamined || 0,
              "mongodb.totalKeysExamined": document.executionStats.totalKeysExamined || 0,
              "mongodb.nReturned": document.executionStats.nReturned || 0,
            });
          }
        } catch (error) {
          console.error("❌ Error in responseHook:", error.message);
        }
      },
    }),
  ],
});

sdk.start();

const hostMetrics = new HostMetrics({ name: process.env.OTEL_SERVICE_NAME || "node-app" });
hostMetrics.start();

console.log("🔍 OpenTelemetry started with custom MongoDB instrumentation");
