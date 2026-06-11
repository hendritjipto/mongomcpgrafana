package com.example.app;

import io.opentelemetry.api.trace.Span;
import io.opentelemetry.instrumentation.annotations.WithSpan;
import org.bson.Document;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.data.mongodb.core.MongoTemplate;
import org.springframework.scheduling.annotation.EnableScheduling;
import org.springframework.scheduling.annotation.Scheduled;
import org.springframework.stereotype.Component;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RestController;

import java.time.Instant;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;

@SpringBootApplication
@EnableScheduling
public class Application {
    public static void main(String[] args) {
        SpringApplication.run(Application.class, args);
    }
}

// Shared MongoDB operations + explain helper. The OTel Java agent auto-traces
// each Mongo command; explainFind adds a dedicated span carrying the plan
// attributes (the equivalent of the Node responseHook / Go explainFind).
@Component
class MongoService {
    static final String COLLECTION = "springboot_events";

    private final MongoTemplate mongoTemplate;

    MongoService(MongoTemplate mongoTemplate) {
        this.mongoTemplate = mongoTemplate;
    }

    void insert() {
        mongoTemplate.getCollection(COLLECTION).insertOne(
            new Document("source", "springboot-app").append("ts", Instant.now().toString()));
    }

    long count() {
        return mongoTemplate.getCollection(COLLECTION).countDocuments();
    }

    List<Document> recent() {
        List<Document> docs = new ArrayList<>();
        mongoTemplate.getCollection(COLLECTION).find()
            .sort(new Document("ts", -1)).limit(10).into(docs);
        return docs;
    }

    @WithSpan("mongodb.explain")
    Document explainFind(Document filter) {
        Document command = new Document("explain",
                new Document("find", COLLECTION).append("filter", filter))
            .append("verbosity", "executionStats");
        Document res = mongoTemplate.getDb().runCommand(command);

        Document qp = res.get("queryPlanner", new Document());
        Document winning = qp.get("winningPlan", new Document());
        Document es = res.get("executionStats", new Document());

        Span span = Span.current();
        span.setAttribute("mongodb.queryShapeHash",
            res.getString("queryShapeHash") != null ? res.getString("queryShapeHash") : "N/A");
        span.setAttribute("mongodb.stage",
            winning.getString("stage") != null ? winning.getString("stage") : "N/A");
        span.setAttribute("mongodb.winningPlan", winning.toJson());
        span.setAttribute("mongodb.totalDocsExamined", longOf(es.get("totalDocsExamined")));
        span.setAttribute("mongodb.totalKeysExamined", longOf(es.get("totalKeysExamined")));
        span.setAttribute("mongodb.nReturned", longOf(es.get("nReturned")));
        return res;
    }

    private static long longOf(Object v) {
        return (v instanceof Number n) ? n.longValue() : 0L;
    }
}

// Background loop. @WithSpan gives it a root span (no HTTP request present),
// so the Mongo insert + explain nest into a trace.
@Component
class MongoWorker {
    private final MongoService svc;

    MongoWorker(MongoService svc) {
        this.svc = svc;
    }

    @WithSpan("background-event-loop")
    @Scheduled(fixedRate = 5000)
    void writeAndCount() {
        svc.insert();
        svc.explainFind(new Document("source", "springboot-app"));
        System.out.println("springboot-app: inserted event, total=" + svc.count());
    }
}

// HTTP interface. The agent auto-creates a root span per request.
@RestController
class EventController {
    private final MongoService svc;
    private final MongoTemplate mongoTemplate;

    EventController(MongoService svc, MongoTemplate mongoTemplate) {
        this.svc = svc;
        this.mongoTemplate = mongoTemplate;
    }

    @GetMapping("/health")
    Map<String, Object> health() {
        mongoTemplate.getDb().runCommand(new Document("ping", 1));
        return Map.of("status", "ok");
    }

    @GetMapping("/events")
    Map<String, Object> events() {
        return Map.of("count", svc.count(), "recent", svc.recent());
    }

    @PostMapping("/events")
    Map<String, Object> create() {
        svc.insert();
        return Map.of("status", "created");
    }

    @GetMapping("/explain")
    Document explain() {
        return svc.explainFind(new Document("source", "springboot-app"));
    }
}
