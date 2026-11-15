package com.gotham.billing.db;

import com.gotham.billing.model.Charge;
import com.gotham.billing.model.ChargeCommand;
import java.nio.file.Files;
import java.nio.file.Path;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.time.Instant;
import java.util.ArrayList;
import java.util.List;
import java.util.Optional;
import java.util.UUID;

/** wayne-billing's own SQLite database. */
public class ChargeStore {
  private final String url;

  public ChargeStore(String dbPath) {
    try {
      Path parent = Path.of(dbPath).toAbsolutePath().getParent();
      if (parent != null) Files.createDirectories(parent);
    } catch (Exception e) {
      throw new IllegalStateException(e);
    }
    this.url = "jdbc:sqlite:" + dbPath;
    try (Connection c = connect(); var st = c.createStatement()) {
      st.execute("PRAGMA journal_mode=WAL");
      st.execute(
          """
          CREATE TABLE IF NOT EXISTS charges (
            charge_id        TEXT PRIMARY KEY,
            idempotency_key  TEXT NOT NULL UNIQUE,
            order_id         TEXT NOT NULL,
            customer_id      TEXT NOT NULL,
            status           TEXT NOT NULL,
            amount_cents     INTEGER NOT NULL,
            currency         TEXT NOT NULL,
            created_at       TEXT NOT NULL
          )""");
    } catch (SQLException e) {
      throw new IllegalStateException("migrate billing db", e);
    }
  }

  private Connection connect() throws SQLException {
    Connection c = DriverManager.getConnection(url);
    try (var st = c.createStatement()) {
      st.execute("PRAGMA busy_timeout=5000");
    }
    return c;
  }

  /** Insert a charge for this idempotency key unless one exists; return whichever row wins. */
  public Charge insertIfAbsent(ChargeCommand cmd, String status) {
    String chargeId = "ch_" + UUID.randomUUID().toString().replace("-", "").substring(0, 16);
    try (Connection c = connect();
        PreparedStatement ps =
            c.prepareStatement(
                """
                INSERT INTO charges (charge_id, idempotency_key, order_id, customer_id, status,
                                     amount_cents, currency, created_at)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?)
                ON CONFLICT(idempotency_key) DO NOTHING""")) {
      ps.setString(1, chargeId);
      ps.setString(2, cmd.idempotencyKey());
      ps.setString(3, cmd.orderId());
      ps.setString(4, cmd.customerId());
      ps.setString(5, status);
      ps.setLong(6, cmd.amountCents());
      ps.setString(7, cmd.currency());
      ps.setString(8, Instant.now().toString());
      ps.executeUpdate();
    } catch (SQLException e) {
      throw new RuntimeException("insert charge", e);
    }
    return findByIdempotencyKey(cmd.idempotencyKey()).orElseThrow();
  }

  public Optional<Charge> findByIdempotencyKey(String key) {
    return queryOne("SELECT * FROM charges WHERE idempotency_key = ?", key);
  }

  public Optional<Charge> findById(String chargeId) {
    return queryOne("SELECT * FROM charges WHERE charge_id = ?", chargeId);
  }

  public List<Charge> list(int limit) {
    try (Connection c = connect();
        PreparedStatement ps =
            c.prepareStatement("SELECT * FROM charges ORDER BY created_at DESC LIMIT ?")) {
      ps.setInt(1, limit);
      List<Charge> out = new ArrayList<>();
      try (ResultSet rs = ps.executeQuery()) {
        while (rs.next()) out.add(map(rs));
      }
      return out;
    } catch (SQLException e) {
      throw new RuntimeException(e);
    }
  }

  private Optional<Charge> queryOne(String sql, String arg) {
    try (Connection c = connect();
        PreparedStatement ps = c.prepareStatement(sql)) {
      ps.setString(1, arg);
      try (ResultSet rs = ps.executeQuery()) {
        return rs.next() ? Optional.of(map(rs)) : Optional.empty();
      }
    } catch (SQLException e) {
      throw new RuntimeException(e);
    }
  }

  private static Charge map(ResultSet rs) throws SQLException {
    return new Charge(
        rs.getString("charge_id"),
        rs.getString("idempotency_key"),
        rs.getString("order_id"),
        rs.getString("customer_id"),
        rs.getString("status"),
        rs.getLong("amount_cents"),
        rs.getString("currency"),
        Charge.MERCHANT,
        rs.getString("created_at"));
  }
}
