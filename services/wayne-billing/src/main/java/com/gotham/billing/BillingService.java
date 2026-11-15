package com.gotham.billing;

import com.fasterxml.jackson.databind.DeserializationFeature;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.PropertyNamingStrategies;
import com.gotham.billing.activities.BillingActivitiesImpl;
import com.gotham.billing.api.ChargeApi;
import com.gotham.billing.db.ChargeStore;
import com.gotham.billing.workflow.ChargeWorkflow;
import com.gotham.billing.workflow.ChargeWorkflowImpl;
import io.javalin.Javalin;
import io.javalin.json.JavalinJackson;
import io.temporal.client.WorkflowClient;
import io.temporal.envconfig.ClientConfigProfile;
import io.temporal.serviceclient.WorkflowServiceStubs;
import io.temporal.worker.Worker;
import io.temporal.worker.WorkerFactory;
import java.util.Map;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

/**
 * wayne-billing: one process = REST API + Temporal worker.
 *
 * <p>Temporal connection comes from envconfig (TEMPORAL_ADDRESS / TEMPORAL_NAMESPACE /
 * TEMPORAL_API_KEY, or a TEMPORAL_PROFILE in temporal.toml).
 */
public class BillingService {
  private static final Logger log = LoggerFactory.getLogger(BillingService.class);

  public static void main(String[] args) throws Exception {
    String dbPath = env("DB_PATH", "data/billing.db");
    int port = Integer.parseInt(env("PORT", "8082"));

    ChargeStore store = new ChargeStore(dbPath);

    ClientConfigProfile profile = ClientConfigProfile.load();
    WorkflowServiceStubs service =
        WorkflowServiceStubs.newServiceStubs(profile.toWorkflowServiceStubsOptions());
    WorkflowClient client =
        WorkflowClient.newInstance(service, profile.toWorkflowClientOptions());
    log.info(
        "Temporal target={} namespace={}",
        service.getOptions().getTarget(),
        client.getOptions().getNamespace());

    WorkerFactory factory = WorkerFactory.newInstance(client);
    Worker worker = factory.newWorker(ChargeWorkflow.TASK_QUEUE);
    worker.registerWorkflowImplementationTypes(ChargeWorkflowImpl.class);
    worker.registerActivitiesImplementations(new BillingActivitiesImpl(store));
    factory.start();

    ObjectMapper http =
        new ObjectMapper()
            .setPropertyNamingStrategy(PropertyNamingStrategies.SNAKE_CASE)
            .configure(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES, false);
    Javalin app =
        Javalin.create(cfg -> cfg.jsonMapper(new JavalinJackson(http, false)))
            .exception(
                Exception.class,
                (e, ctx) -> {
                  log.error("unhandled error on {}", ctx.path(), e);
                  ctx.status(500).json(Map.of("error", "INTERNAL", "message", String.valueOf(e)));
                });
    new ChargeApi(client, store).register(app);
    app.start(port);
    log.info("wayne-billing listening on :{} taskQueue={}", port, ChargeWorkflow.TASK_QUEUE);

    Runtime.getRuntime()
        .addShutdownHook(
            new Thread(
                () -> {
                  log.info("shutting down");
                  app.stop();
                  factory.shutdown();
                  service.shutdown();
                }));
  }

  private static String env(String k, String def) {
    String v = System.getenv(k);
    return v == null || v.isBlank() ? def : v;
  }
}
