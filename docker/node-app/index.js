// Express HTTP interface backed by MongoDB. Auto-instrumentation traces each
// request (Express + HTTP) with the MongoDB operations as child spans.
import express from "express";
import { MongoClient } from "mongodb";
import { trace } from "@opentelemetry/api";

const tracer = trace.getTracer("node-app");

const uri = process.env.MONGODB_URI;
const port = process.env.PORT || 8080;
const client = new MongoClient(uri);

let collection;

const app = express();
app.use(express.json());

app.get("/health", async (req, res) => {
  try {
    await client.db().command({ ping: 1 });
    res.json({ status: "ok" });
  } catch (err) {
    res.status(503).json({ status: "error", error: err.message });
  }
});

app.get("/events", async (req, res) => {
  const recent = await collection.find().sort({ ts: -1 }).limit(10).toArray();
  const count = await collection.countDocuments();
  res.json({ count, recent });
});

app.post("/events", async (req, res) => {
  const doc = { source: "node-app", ts: new Date(), ...req.body };
  const result = await collection.insertOne(doc);
  res.status(201).json({ insertedId: result.insertedId });
});

// On-demand read with explain. The .explain() call makes the driver issue an
// `explain` command, which the MongoDBInstrumentation responseHook enriches
// with query-plan attributes (stage, docsExamined, queryShapeHash, ...).
app.get("/explain", async (req, res) => {
  const plan = await collection.find({ source: "node-app" }).explain("executionStats");
  res.json(plan);
});

async function start() {
  await client.connect();
  console.log("✅ node-app connected to MongoDB");
  collection = client.db().collection("events");

  // Background traffic so telemetry flows without manual requests. The timer
  // callback has no active span, so start a root span here for the Mongo
  // operations to attach to (otherwise the loop produces no trace).
  setInterval(() => {
    tracer.startActiveSpan("background-event-loop", async (span) => {
      try {
        await collection.insertOne({ source: "node-app", ts: new Date() });
        // Read-with-explain → triggers the plan-capturing responseHook.
        await collection.find({ source: "node-app" }).explain("executionStats");
      } catch (err) {
        span.recordException(err);
        console.error("node-app loop error:", err.message);
      } finally {
        span.end();
      }
    });
  }, 5000);

  app.listen(port, () => console.log(`🚀 node-app listening on :${port}`));
}

start().catch((err) => {
  console.error("node-app fatal:", err);
  process.exit(1);
});
