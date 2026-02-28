package com.swarmblackjack.bank;

import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpHandler;
import com.sun.net.httpserver.HttpServer;

import java.io.*;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.math.BigDecimal;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.sql.*;
import java.time.Instant;
import java.util.UUID;
import java.util.logging.Logger;
import redis.clients.jedis.Jedis;
import redis.clients.jedis.JedisPool;
import redis.clients.jedis.JedisPoolConfig;

/**
 * Bank Service
 * Language: Java
 *
 * Why Java? Financial arithmetic. BigDecimal everywhere — no floats near money.
 * Java's type system makes accidental float arithmetic a compile error when
 * you're disciplined about it.
 *
 * Owns the only source of truth for player chip balances.
 * Isolated PostgreSQL instance — only this service has credentials.
 * Game-state delegates ALL financial operations here.
 *
 * Endpoints:
 *   POST /account           — register player with starting balance (idempotent)
 *   GET  /balance?playerId= — current balance
 *   GET  /transactions?playerId=&limit= — transaction history
 *   POST /bet               — deduct bet, return transaction_id
 *   POST /payout            — settle transaction (win/loss/push)
 *   POST /deposit           — add chips
 *   POST /withdraw          — remove chips
 *   GET  /health
 *   POST /dev/reset         — DEV ONLY: wipe all data, re-seed demo player
 */
public class BankService {

    private static final Logger log = Logger.getLogger(BankService.class.getName());

    static final String DEMO_PLAYER_ID = "player-00000000-0000-0000-0000-000000000001";
    static final String STARTING_BALANCE = "1000.00";

    // ── Database ──────────────────────────────────────────────────────────────

    /**
     * Simple connection manager — reconnects on failure.
     * Bank operations are naturally serialized (one game loop, sequential bets)
     * so a single validated connection is sufficient for the PoC.
     * Production: swap for HikariCP with a 5-connection pool.
     */
    static class DB {
        final String url;
        private final String user;
        private final String password;
        private Connection conn;

        DB(String host, String port, String name, String user, String password) {
            this.url      = "jdbc:postgresql://" + host + ":" + port + "/" + name;
            this.user     = user;
            this.password = password;
        }

        synchronized Connection get() throws SQLException {
            if (conn == null || !conn.isValid(2)) {
                if (conn != null) { try { conn.close(); } catch (Exception ignored) {} }
                conn = DriverManager.getConnection(url, user, password);
                log.info("[bank-db] Connection established");
            }
            return conn;
        }

        synchronized void migrate() throws SQLException {
            Connection c = get();
            try (Statement st = c.createStatement()) {
                st.execute("""
                    CREATE TABLE IF NOT EXISTS accounts (
                        player_id   VARCHAR(100) PRIMARY KEY,
                        balance     NUMERIC(15,2) NOT NULL DEFAULT 1000.00,
                        created_at  TIMESTAMPTZ   NOT NULL DEFAULT NOW()
                    )
                """);
                st.execute("""
                    CREATE TABLE IF NOT EXISTS transactions (
                        id             UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
                        player_id      VARCHAR(100) NOT NULL REFERENCES accounts(player_id),
                        type           VARCHAR(30)  NOT NULL,
                        amount         NUMERIC(15,2) NOT NULL,
                        balance_before NUMERIC(15,2) NOT NULL,
                        balance_after  NUMERIC(15,2) NOT NULL,
                        ref_id         VARCHAR(100),
                        note           VARCHAR(255),
                        created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW()
                    )
                """);
                st.execute("""
                    CREATE TABLE IF NOT EXISTS open_bets (
                        transaction_id VARCHAR(100)  PRIMARY KEY,
                        player_id      VARCHAR(100)  NOT NULL REFERENCES accounts(player_id),
                        amount         NUMERIC(15,2) NOT NULL,
                        created_at     TIMESTAMPTZ   NOT NULL DEFAULT NOW()
                    )
                """);
                st.execute("""
                    CREATE INDEX IF NOT EXISTS idx_transactions_player
                        ON transactions(player_id, created_at DESC)
                """);
                log.info("[bank-db] Schema ready");
            }
        }

        /** Seed demo player if not present */
        synchronized void seedDemoPlayer() throws SQLException {
            Connection c = get();
            try (PreparedStatement ps = c.prepareStatement(
                    "INSERT INTO accounts(player_id, balance) VALUES(?,?) ON CONFLICT DO NOTHING")) {
                ps.setString(1, DEMO_PLAYER_ID);
                ps.setBigDecimal(2, new BigDecimal(STARTING_BALANCE));
                int rows = ps.executeUpdate();
                if (rows > 0) log.info("[bank-db] Demo player seeded: " + DEMO_PLAYER_ID);
            }
        }
    }

    static DB db;
    static JedisPool jedisPool;

    // ── Server Setup ──────────────────────────────────────────────────────────

    public static void main(String[] args) throws Exception {
        int port = Integer.parseInt(System.getenv().getOrDefault("PORT", "3005"));
        String documentServiceUrl = System.getenv().getOrDefault("DOCUMENT_SERVICE_URL", "http://document-service:3011");
        ExportHandler.setDocumentServiceUrl(documentServiceUrl);

        // Credentials come from bank.env — never hardcoded, never in a connection string
        String dbHost = System.getenv().getOrDefault("BANK_DB_HOST", "bank-db");
        String dbPort = System.getenv().getOrDefault("BANK_DB_PORT", "5432");
        String dbName = System.getenv().getOrDefault("BANK_DB_NAME", "bankdb");
        String dbUser = System.getenv().getOrDefault("BANK_DB_USER", "bankuser");
        String dbPass = System.getenv().getOrDefault("BANK_DB_PASSWORD", "");

        // Redis for balance pub-sub
        String redisHost = System.getenv().getOrDefault("REDIS_HOST", "redis");
        int    redisPort = Integer.parseInt(System.getenv().getOrDefault("REDIS_PORT", "6379"));
        jedisPool = new JedisPool(new JedisPoolConfig(), redisHost, redisPort);
        log.info("[bank] Redis pool initialized: " + redisHost + ":" + redisPort);

        // Wait for DB with retries
        db = new DB(dbHost, dbPort, dbName, dbUser, dbPass);
        waitForDb();
        db.migrate();
        db.seedDemoPlayer();

        HttpServer server = HttpServer.create(new InetSocketAddress(port), 0);
        server.createContext("/health",       new HealthHandler());
        server.createContext("/account",      new AccountHandler());
        server.createContext("/balance",      new BalanceHandler());
        server.createContext("/transactions", new TransactionsHandler());
        server.createContext("/bet",          new BetHandler());
        server.createContext("/payout",       new PayoutHandler());
        server.createContext("/deposit",      new DepositHandler());
        server.createContext("/withdraw",     new WithdrawHandler());
        server.createContext("/export",        new ExportHandler());
        server.createContext("/dev/reset",    new DevResetHandler());
        server.setExecutor(null);
        server.start();

        log.info(String.format("Bank Service (Java) started on :%d", port));
        log.info("   BigDecimal arithmetic — no floats near money. Ever.");
        log.info("   PostgreSQL backend — balances survive restarts.");
    }

    static void waitForDb() {
        log.info("[bank-db] Waiting for database...");
        for (int i = 1; i <= 15; i++) {
            try {
                db.get();
                log.info("[bank-db] Ready after attempt " + i);
                return;
            } catch (SQLException e) {
                Throwable cause = e.getCause() != null ? e.getCause() : e;
                log.warning(String.format("[bank-db] Attempt %d/15: %s | cause: %s | url: %s",
                    i, e.getMessage(), cause.toString(), db.url));
                try { Thread.sleep(2000); } catch (InterruptedException ie) { Thread.currentThread().interrupt(); }
            }
        }
        throw new RuntimeException("[bank-db] Could not connect after 15 attempts");
    }

    // ── Handlers ──────────────────────────────────────────────────────────────

    static class HealthHandler implements HttpHandler {
        @Override
        public void handle(HttpExchange ex) throws IOException {
            boolean dbOk = false;
            int playerCount = 0;
            int openBetCount = 0;
            try {
                Connection c = db.get();
                try (Statement st = c.createStatement()) {
                    ResultSet rs = st.executeQuery("SELECT COUNT(*) FROM accounts");
                    if (rs.next()) playerCount = rs.getInt(1);
                    rs = st.executeQuery("SELECT COUNT(*) FROM open_bets");
                    if (rs.next()) openBetCount = rs.getInt(1);
                }
                dbOk = true;
            } catch (SQLException e) {
                log.warning("[health] DB check failed: " + e.getMessage());
            }
            sendJson(ex, 200, String.format(
                "{\"status\":\"%s\",\"service\":\"bank-service\",\"language\":\"Java\"," +
                "\"db\":\"%s\",\"players\":%d,\"open_bets\":%d," +
                "\"note\":\"BigDecimal only — float arithmetic is a compile-time error here\"}",
                dbOk ? "healthy" : "degraded",
                dbOk ? "connected" : "disconnected",
                playerCount, openBetCount
            ));
        }
    }

    /**
     * POST /account
     * Register a player. Idempotent — existing players are not modified.
     * Body: {"playerId": "...", "startingBalance": "1000.00"}
     */
    static class AccountHandler implements HttpHandler {
        @Override
        public void handle(HttpExchange ex) throws IOException {
            if (handleOptions(ex)) return;
            if (!"POST".equals(ex.getRequestMethod())) {
                sendJson(ex, 405, "{\"error\":\"method not allowed\"}"); return;
            }
            String body = readBody(ex);
            String playerId = extractJsonString(body, "playerId");
            String startingStr = extractJsonString(body, "startingBalance");
            if (playerId == null) {
                sendJson(ex, 400, "{\"error\":\"playerId required\"}"); return;
            }
            BigDecimal starting = new BigDecimal(startingStr != null ? startingStr : STARTING_BALANCE);
            try {
                Connection c = db.get();
                // Idempotent insert
                try (PreparedStatement ps = c.prepareStatement(
                        "INSERT INTO accounts(player_id, balance) VALUES(?,?) ON CONFLICT DO NOTHING")) {
                    ps.setString(1, playerId);
                    ps.setBigDecimal(2, starting);
                    ps.executeUpdate();
                }
                BigDecimal balance;
                try (PreparedStatement ps = c.prepareStatement(
                        "SELECT balance FROM accounts WHERE player_id=?")) {
                    ps.setString(1, playerId);
                    ResultSet rs = ps.executeQuery();
                    if (!rs.next()) { sendJson(ex, 500, "{\"error\":\"account creation failed\"}"); return; }
                    balance = rs.getBigDecimal(1);
                }
                log.info(String.format("Account: player=%s balance=%s", playerId, balance));
                sendJson(ex, 200, String.format(
                    "{\"playerId\":\"%s\",\"balance\":%s,\"currency\":\"chips\"}",
                    playerId, balance.toPlainString()
                ));
            } catch (SQLException e) {
                log.severe("Account error: " + e.getMessage());
                sendJson(ex, 500, "{\"error\":\"database error\"}");
            }
        }
    }

    /**
     * GET /balance?playerId=
     */
    static class BalanceHandler implements HttpHandler {
        @Override
        public void handle(HttpExchange ex) throws IOException {
            if (handleOptions(ex)) return;
            String playerId = parseQueryParam(ex.getRequestURI().getQuery(), "playerId");
            if (playerId == null) {
                sendJson(ex, 400, "{\"error\":\"playerId required\"}"); return;
            }
            try {
                Connection c = db.get();
                try (PreparedStatement ps = c.prepareStatement(
                        "SELECT balance FROM accounts WHERE player_id=?")) {
                    ps.setString(1, playerId);
                    ResultSet rs = ps.executeQuery();
                    if (!rs.next()) { sendJson(ex, 404, "{\"error\":\"player not found\"}"); return; }
                    BigDecimal balance = rs.getBigDecimal(1);
                    sendJson(ex, 200, String.format(
                        "{\"playerId\":\"%s\",\"balance\":%s,\"currency\":\"chips\"}",
                        playerId, balance.toPlainString()
                    ));
                }
            } catch (SQLException e) {
                log.severe("Balance error: " + e.getMessage());
                sendJson(ex, 500, "{\"error\":\"database error\"}");
            }
        }
    }

    /**
     * GET /transactions?playerId=&limit=50
     * Returns transaction history, most recent first.
     */
    static class TransactionsHandler implements HttpHandler {
        @Override
        public void handle(HttpExchange ex) throws IOException {
            if (handleOptions(ex)) return;
            String query = ex.getRequestURI().getQuery();
            String playerId = parseQueryParam(query, "playerId");
            String limitStr = parseQueryParam(query, "limit");
            if (playerId == null) {
                sendJson(ex, 400, "{\"error\":\"playerId required\"}"); return;
            }
            int limit = 50;
            try { if (limitStr != null) limit = Math.min(200, Integer.parseInt(limitStr)); }
            catch (NumberFormatException ignored) {}
            try {
                Connection c = db.get();
                try (PreparedStatement ps = c.prepareStatement(
                        "SELECT id, type, amount, balance_before, balance_after, ref_id, note, created_at " +
                        "FROM transactions WHERE player_id=? ORDER BY created_at DESC LIMIT ?")) {
                    ps.setString(1, playerId);
                    ps.setInt(2, limit);
                    ResultSet rs = ps.executeQuery();
                    StringBuilder sb = new StringBuilder("[");
                    boolean first = true;
                    while (rs.next()) {
                        if (!first) sb.append(",");
                        first = false;
                        String refId = rs.getString("ref_id");
                        String note  = rs.getString("note");
                        sb.append(String.format(
                            "{\"id\":\"%s\",\"type\":\"%s\",\"amount\":%s," +
                            "\"balanceBefore\":%s,\"balanceAfter\":%s," +
                            "\"refId\":%s,\"note\":%s,\"createdAt\":\"%s\"}",
                            rs.getString("id"),
                            rs.getString("type"),
                            rs.getBigDecimal("amount").toPlainString(),
                            rs.getBigDecimal("balance_before").toPlainString(),
                            rs.getBigDecimal("balance_after").toPlainString(),
                            refId != null ? "\"" + refId + "\"" : "null",
                            note  != null ? "\"" + note  + "\"" : "null",
                            rs.getTimestamp("created_at").toInstant().toString()
                        ));
                    }
                    sb.append("]");
                    sendJson(ex, 200, String.format(
                        "{\"playerId\":\"%s\",\"transactions\":%s}", playerId, sb));
                }
            } catch (SQLException e) {
                log.severe("Transactions error: " + e.getMessage());
                sendJson(ex, 500, "{\"error\":\"database error\"}");
            }
        }
    }

    /**
     * POST /bet
     * Deduct bet from balance. Returns transaction_id for later settlement.
     * Body: {"playerId": "...", "amount": "50.00"}
     */
    static class BetHandler implements HttpHandler {
        @Override
        public void handle(HttpExchange ex) throws IOException {
            if (handleOptions(ex)) return;
            if (!"POST".equals(ex.getRequestMethod())) {
                sendJson(ex, 405, "{\"error\":\"method not allowed\"}"); return;
            }
            String body = readBody(ex);
            String playerId = extractJsonString(body, "playerId");
            String amountStr = extractJsonString(body, "amount");
            if (playerId == null || amountStr == null) {
                sendJson(ex, 400, "{\"error\":\"playerId and amount required\"}"); return;
            }
            BigDecimal amount;
            try {
                amount = new BigDecimal(amountStr);
                if (amount.compareTo(BigDecimal.ZERO) <= 0) throw new NumberFormatException();
            } catch (NumberFormatException e) {
                sendJson(ex, 400, "{\"error\":\"invalid amount\"}"); return;
            }
            try {
                Connection c = db.get();
                c.setAutoCommit(false);
                try {
                    // Lock row and read balance
                    BigDecimal current;
                    try (PreparedStatement ps = c.prepareStatement(
                            "SELECT balance FROM accounts WHERE player_id=? FOR UPDATE")) {
                        ps.setString(1, playerId);
                        ResultSet rs = ps.executeQuery();
                        if (!rs.next()) {
                            c.rollback();
                            sendJson(ex, 404, "{\"error\":\"player not found\"}"); return;
                        }
                        current = rs.getBigDecimal(1);
                    }
                    if (current.compareTo(amount) < 0) {
                        // Demo player auto-replenish: reset to starting balance instead of failing
                        if (playerId.equals(DEMO_PLAYER_ID)) {
                            current = new BigDecimal(STARTING_BALANCE);
                            try (PreparedStatement ps = c.prepareStatement(
                                    "UPDATE accounts SET balance=? WHERE player_id=?")) {
                                ps.setBigDecimal(1, current);
                                ps.setString(2, DEMO_PLAYER_ID);
                                ps.executeUpdate();
                            }
                            log.info("[bank] Demo player replenished to " + STARTING_BALANCE);
                        } else {
                            c.rollback();
                            sendJson(ex, 409, String.format(
                                "{\"error\":\"insufficient funds\",\"balance\":%s,\"requested\":%s}",
                                current.toPlainString(), amount.toPlainString()
                            )); return;
                        }
                    }
                    BigDecimal newBalance = current.subtract(amount);
                    // Deduct balance
                    try (PreparedStatement ps = c.prepareStatement(
                            "UPDATE accounts SET balance=? WHERE player_id=?")) {
                        ps.setBigDecimal(1, newBalance);
                        ps.setString(2, playerId);
                        ps.executeUpdate();
                    }
                    // Record open bet
                    String txId = UUID.randomUUID().toString();
                    try (PreparedStatement ps = c.prepareStatement(
                            "INSERT INTO open_bets(transaction_id, player_id, amount) VALUES(?,?,?)")) {
                        ps.setString(1, txId);
                        ps.setString(2, playerId);
                        ps.setBigDecimal(3, amount);
                        ps.executeUpdate();
                    }
                    // Write transaction record
                    try (PreparedStatement ps = c.prepareStatement(
                            "INSERT INTO transactions(player_id,type,amount,balance_before,balance_after,ref_id) " +
                            "VALUES(?,?,?,?,?,?)")) {
                        ps.setString(1, playerId);
                        ps.setString(2, "bet");
                        ps.setBigDecimal(3, amount);
                        ps.setBigDecimal(4, current);
                        ps.setBigDecimal(5, newBalance);
                        ps.setString(6, txId);
                        ps.executeUpdate();
                    }
                    c.commit();
                    log.info(String.format("Bet: player=%s amount=%s txId=%s newBalance=%s",
                        playerId, amount, txId, newBalance));
                    sendJson(ex, 200, String.format(
                        "{\"transactionId\":\"%s\",\"playerId\":\"%s\",\"amount\":%s,\"newBalance\":%s}",
                        txId, playerId, amount.toPlainString(), newBalance.toPlainString()
                    ));
                } catch (SQLException e) {
                    c.rollback();
                    throw e;
                } finally {
                    c.setAutoCommit(true);
                }
            } catch (SQLException e) {
                log.severe("Bet error: " + e.getMessage());
                sendJson(ex, 500, "{\"error\":\"database error\"}");
            }
        }
    }

    /**
     * POST /payout
     * Settle a bet transaction.
     * Body: {"transactionId": "...", "result": "win|loss|push"}
     *   win  → return 2x bet (original bet + winnings)
     *   push → return 1x bet (original bet back)
     *   loss → nothing returned (already deducted at bet time)
     */
    static class PayoutHandler implements HttpHandler {
        @Override
        public void handle(HttpExchange ex) throws IOException {
            if (handleOptions(ex)) return;
            if (!"POST".equals(ex.getRequestMethod())) {
                sendJson(ex, 405, "{\"error\":\"method not allowed\"}"); return;
            }
            String body = readBody(ex);
            String txId  = extractJsonString(body, "transactionId");
            String result = extractJsonString(body, "result");
            if (txId == null || result == null) {
                sendJson(ex, 400, "{\"error\":\"transactionId and result required\"}"); return;
            }
            try {
                Connection c = db.get();
                c.setAutoCommit(false);
                try {
                    // Fetch and remove open bet atomically
                    String playerId;
                    BigDecimal betAmount;
                    try (PreparedStatement ps = c.prepareStatement(
                            "DELETE FROM open_bets WHERE transaction_id=? RETURNING player_id, amount")) {
                        ps.setString(1, txId);
                        ResultSet rs = ps.executeQuery();
                        if (!rs.next()) {
                            c.rollback();
                            sendJson(ex, 404, "{\"error\":\"transaction not found or already settled\"}"); return;
                        }
                        playerId  = rs.getString(1);
                        betAmount = rs.getBigDecimal(2);
                    }
                    BigDecimal returned;
                    String txType;
                    switch (result.toLowerCase()) {
                        case "win":
                            returned = betAmount.multiply(new BigDecimal("2"));
                            txType = "payout_win";
                            break;
                        case "push":
                            returned = betAmount;
                            txType = "payout_push";
                            break;
                        case "loss":
                            returned = BigDecimal.ZERO;
                            txType = "payout_loss";
                            break;
                        default:
                            c.rollback();
                            sendJson(ex, 400, "{\"error\":\"result must be win, loss, or push\"}");
                            return;
                    }
                    // Read current balance (lock row)
                    BigDecimal current;
                    try (PreparedStatement ps = c.prepareStatement(
                            "SELECT balance FROM accounts WHERE player_id=? FOR UPDATE")) {
                        ps.setString(1, playerId);
                        ResultSet rs = ps.executeQuery();
                        rs.next();
                        current = rs.getBigDecimal(1);
                    }
                    BigDecimal newBalance = current.add(returned);
                    // Update balance
                    try (PreparedStatement ps = c.prepareStatement(
                            "UPDATE accounts SET balance=? WHERE player_id=?")) {
                        ps.setBigDecimal(1, newBalance);
                        ps.setString(2, playerId);
                        ps.executeUpdate();
                    }
                    // Write transaction record
                    try (PreparedStatement ps = c.prepareStatement(
                            "INSERT INTO transactions(player_id,type,amount,balance_before,balance_after,ref_id) " +
                            "VALUES(?,?,?,?,?,?)")) {
                        ps.setString(1, playerId);
                        ps.setString(2, txType);
                        ps.setBigDecimal(3, returned);
                        ps.setBigDecimal(4, current);
                        ps.setBigDecimal(5, newBalance);
                        ps.setString(6, txId);
                        ps.executeUpdate();
                    }
                    c.commit();
                    log.info(String.format("Payout: player=%s txId=%s result=%s returned=%s newBalance=%s",
                        playerId, txId, result, returned, newBalance));

                    // Publish balance update to Redis for real-time UI
                    try (Jedis jedis = jedisPool.getResource()) {
                        String event = String.format(
                            "{\"playerId\":\"%s\",\"balance\":%s}",
                            playerId, newBalance.toPlainString()
                        );
                        jedis.publish("swarm:balance", event);
                    } catch (Exception e) {
                        log.warning("[bank] Redis publish failed (non-fatal): " + e.getMessage());
                    }

                    sendJson(ex, 200, String.format(
                        "{\"transactionId\":\"%s\",\"playerId\":\"%s\",\"result\":\"%s\"," +
                        "\"betAmount\":%s,\"returned\":%s,\"newBalance\":%s}",
                        txId, playerId, result,
                        betAmount.toPlainString(), returned.toPlainString(), newBalance.toPlainString()
                    ));
                } catch (SQLException e) {
                    c.rollback();
                    throw e;
                } finally {
                    c.setAutoCommit(true);
                }
            } catch (SQLException e) {
                log.severe("Payout error: " + e.getMessage());
                sendJson(ex, 500, "{\"error\":\"database error\"}");
            }
        }
    }

    /**
     * POST /deposit — add chips
     */
    static class DepositHandler implements HttpHandler {
        @Override
        public void handle(HttpExchange ex) throws IOException {
            if (handleOptions(ex)) return;
            if (!"POST".equals(ex.getRequestMethod())) {
                sendJson(ex, 405, "{\"error\":\"method not allowed\"}"); return;
            }
            String body = readBody(ex);
            String playerId = extractJsonString(body, "playerId");
            String amountStr = extractJsonString(body, "amount");
            if (playerId == null || amountStr == null) {
                sendJson(ex, 400, "{\"error\":\"playerId and amount required\"}"); return;
            }
            BigDecimal amount;
            try {
                amount = new BigDecimal(amountStr);
                if (amount.compareTo(BigDecimal.ZERO) <= 0) throw new NumberFormatException();
            } catch (NumberFormatException e) {
                sendJson(ex, 400, "{\"error\":\"invalid amount\"}"); return;
            }
            try {
                Connection c = db.get();
                c.setAutoCommit(false);
                try {
                    BigDecimal current;
                    try (PreparedStatement ps = c.prepareStatement(
                            "SELECT balance FROM accounts WHERE player_id=? FOR UPDATE")) {
                        ps.setString(1, playerId);
                        ResultSet rs = ps.executeQuery();
                        if (!rs.next()) { c.rollback(); sendJson(ex, 404, "{\"error\":\"player not found\"}"); return; }
                        current = rs.getBigDecimal(1);
                    }
                    BigDecimal newBalance = current.add(amount);
                    try (PreparedStatement ps = c.prepareStatement(
                            "UPDATE accounts SET balance=? WHERE player_id=?")) {
                        ps.setBigDecimal(1, newBalance);
                        ps.setString(2, playerId);
                        ps.executeUpdate();
                    }
                    try (PreparedStatement ps = c.prepareStatement(
                            "INSERT INTO transactions(player_id,type,amount,balance_before,balance_after) VALUES(?,?,?,?,?)")) {
                        ps.setString(1, playerId); ps.setString(2, "deposit");
                        ps.setBigDecimal(3, amount); ps.setBigDecimal(4, current); ps.setBigDecimal(5, newBalance);
                        ps.executeUpdate();
                    }
                    c.commit();
                    log.info(String.format("Deposit: player=%s amount=%s newBalance=%s", playerId, amount, newBalance));
                    sendJson(ex, 200, String.format(
                        "{\"playerId\":\"%s\",\"deposited\":%s,\"newBalance\":%s}",
                        playerId, amount.toPlainString(), newBalance.toPlainString()
                    ));
                } catch (SQLException e) { c.rollback(); throw e; }
                finally { c.setAutoCommit(true); }
            } catch (SQLException e) {
                log.severe("Deposit error: " + e.getMessage());
                sendJson(ex, 500, "{\"error\":\"database error\"}");
            }
        }
    }

    /**
     * POST /withdraw — remove chips
     */
    static class WithdrawHandler implements HttpHandler {
        @Override
        public void handle(HttpExchange ex) throws IOException {
            if (handleOptions(ex)) return;
            if (!"POST".equals(ex.getRequestMethod())) {
                sendJson(ex, 405, "{\"error\":\"method not allowed\"}"); return;
            }
            String body = readBody(ex);
            String playerId = extractJsonString(body, "playerId");
            String amountStr = extractJsonString(body, "amount");
            if (playerId == null || amountStr == null) {
                sendJson(ex, 400, "{\"error\":\"playerId and amount required\"}"); return;
            }
            BigDecimal amount;
            try {
                amount = new BigDecimal(amountStr);
                if (amount.compareTo(BigDecimal.ZERO) <= 0) throw new NumberFormatException();
            } catch (NumberFormatException e) {
                sendJson(ex, 400, "{\"error\":\"invalid amount\"}"); return;
            }
            try {
                Connection c = db.get();
                c.setAutoCommit(false);
                try {
                    BigDecimal current;
                    try (PreparedStatement ps = c.prepareStatement(
                            "SELECT balance FROM accounts WHERE player_id=? FOR UPDATE")) {
                        ps.setString(1, playerId);
                        ResultSet rs = ps.executeQuery();
                        if (!rs.next()) { c.rollback(); sendJson(ex, 404, "{\"error\":\"player not found\"}"); return; }
                        current = rs.getBigDecimal(1);
                    }
                    if (current.compareTo(amount) < 0) {
                        c.rollback();
                        sendJson(ex, 409, "{\"error\":\"insufficient funds\"}"); return;
                    }
                    BigDecimal newBalance = current.subtract(amount);
                    try (PreparedStatement ps = c.prepareStatement(
                            "UPDATE accounts SET balance=? WHERE player_id=?")) {
                        ps.setBigDecimal(1, newBalance); ps.setString(2, playerId); ps.executeUpdate();
                    }
                    try (PreparedStatement ps = c.prepareStatement(
                            "INSERT INTO transactions(player_id,type,amount,balance_before,balance_after) VALUES(?,?,?,?,?)")) {
                        ps.setString(1, playerId); ps.setString(2, "withdrawal");
                        ps.setBigDecimal(3, amount); ps.setBigDecimal(4, current); ps.setBigDecimal(5, newBalance);
                        ps.executeUpdate();
                    }
                    c.commit();
                    log.info(String.format("Withdrawal: player=%s amount=%s newBalance=%s", playerId, amount, newBalance));
                    sendJson(ex, 200, String.format(
                        "{\"playerId\":\"%s\",\"withdrawn\":%s,\"newBalance\":%s}",
                        playerId, amount.toPlainString(), newBalance.toPlainString()
                    ));
                } catch (SQLException e) { c.rollback(); throw e; }
                finally { c.setAutoCommit(true); }
            } catch (SQLException e) {
                log.severe("Withdrawal error: " + e.getMessage());
                sendJson(ex, 500, "{\"error\":\"database error\"}");
            }
        }
    }

    // ── Utilities ─────────────────────────────────────────────────────────────

    static boolean handleOptions(HttpExchange ex) throws IOException {
        if ("OPTIONS".equals(ex.getRequestMethod())) {
            ex.getResponseHeaders().set("Access-Control-Allow-Origin", "*");
            ex.getResponseHeaders().set("Access-Control-Allow-Methods", "GET, POST, OPTIONS");
            ex.getResponseHeaders().set("Access-Control-Allow-Headers", "Content-Type");
            ex.sendResponseHeaders(204, -1);
            return true;
        }
        return false;
    }

    static void sendJson(HttpExchange ex, int status, String json) throws IOException {
        byte[] bytes = json.getBytes(StandardCharsets.UTF_8);
        ex.getResponseHeaders().set("Content-Type", "application/json");
        ex.getResponseHeaders().set("Access-Control-Allow-Origin", "*");
        ex.sendResponseHeaders(status, bytes.length);
        try (OutputStream os = ex.getResponseBody()) { os.write(bytes); }
    }

    static String readBody(HttpExchange ex) throws IOException {
        try (InputStream is = ex.getRequestBody()) {
            return new String(is.readAllBytes(), StandardCharsets.UTF_8);
        }
    }

    static String parseQueryParam(String query, String key) {
        if (query == null) return null;
        for (String part : query.split("&")) {
            String[] kv = part.split("=", 2);
            if (kv.length == 2 && kv[0].equals(key)) return kv[1];
        }
        return null;
    }

    static String extractJsonString(String json, String key) {
        if (json == null) return null;
        String search = "\"" + key + "\":\"";
        int start = json.indexOf(search);
        if (start < 0) return null;
        start += search.length();
        int end = json.indexOf("\"", start);
        if (end < 0) return null;
        return json.substring(start, end);
    }

    // ── Export Handler ─────────────────────────────────────────────────────────

    /**
     * GET /export?playerId=&limit=200
     * Fetches transactions from DB, calls document-service, streams PDF back.
     * The UI never calls document-service directly - bank-service owns the data.
     */
    static class ExportHandler implements HttpHandler {
        private static String documentServiceUrl = "http://document-service:3011";

        static void setDocumentServiceUrl(String url) { documentServiceUrl = url; }

        @Override
        public void handle(HttpExchange ex) throws IOException {
            if (handleOptions(ex)) return;
            if (!"GET".equals(ex.getRequestMethod())) {
                sendJson(ex, 405, "{\"error\":\"method not allowed\"}"); return;
            }

            String query = ex.getRequestURI().getQuery();
            String playerId = parseQueryParam(query, "playerId");
            String limitStr = parseQueryParam(query, "limit");
            if (playerId == null) {
                sendJson(ex, 400, "{\"error\":\"playerId required\"}"); return;
            }

            int limit = 200;
            try { if (limitStr != null) limit = Math.min(500, Integer.parseInt(limitStr)); }
            catch (NumberFormatException ignored) {}

            BigDecimal currentBalance = BigDecimal.ZERO;
            StringBuilder rowsSb = new StringBuilder();
            int txCount = 0;
            BigDecimal netResult = BigDecimal.ZERO;

            try {
                Connection c = db.get();
                try (PreparedStatement ps = c.prepareStatement(
                        "SELECT balance FROM accounts WHERE player_id=?")) {
                    ps.setString(1, playerId);
                    ResultSet rs = ps.executeQuery();
                    if (!rs.next()) { sendJson(ex, 404, "{\"error\":\"player not found\"}"); return; }
                    currentBalance = rs.getBigDecimal(1);
                }
                try (PreparedStatement ps = c.prepareStatement(
                        "SELECT id, type, amount, balance_before, balance_after, created_at " +
                        "FROM transactions WHERE player_id=? ORDER BY created_at DESC LIMIT ?")) {
                    ps.setString(1, playerId);
                    ps.setInt(2, limit);
                    ResultSet rs = ps.executeQuery();
                    while (rs.next()) {
                        String id = rs.getString("id").substring(0, 8);
                        String type = rs.getString("type");
                        BigDecimal amount = rs.getBigDecimal("amount");
                        BigDecimal before = rs.getBigDecimal("balance_before");
                        BigDecimal after  = rs.getBigDecimal("balance_after");
                        String ts = rs.getTimestamp("created_at").toInstant().toString();
                        if (rowsSb.length() > 0) rowsSb.append("\n");
                        rowsSb.append(String.format("%s,%s,%s,%s,%s,%s",
                            id, type, amount.toPlainString(),
                            before.toPlainString(), after.toPlainString(), ts));
                        if (type.contains("win") || type.equals("deposit") || type.equals("payout_push")) {
                            netResult = netResult.add(amount);
                        } else if (type.equals("bet") || type.contains("loss") || type.equals("withdrawal")) {
                            netResult = netResult.subtract(amount);
                        }
                        txCount++;
                    }
                }
            } catch (SQLException e) {
                log.severe("Export DB error: " + e.getMessage());
                sendJson(ex, 500, "{\"error\":\"database error\"}"); return;
            }

            String now = Instant.now().toString();
            String sign = netResult.compareTo(BigDecimal.ZERO) >= 0 ? "+" : "";

            // Build document-service block payload
            String summaryRow = String.format("%d,%s%s,%s",
                txCount, sign, netResult.toPlainString(), currentBalance.toPlainString());

            StringBuilder docJson = new StringBuilder();
            docJson.append("{");
            docJson.append("\"caller\":\"bank-service\",");
            docJson.append("\"title\":\"Transaction History\",");
            docJson.append("\"heading\":\"Swarm Blackjack - Player Ledger\",");
            docJson.append("\"sub_heading\":\"").append(playerId).append("\",");
            docJson.append("\"blocks\":[");
            docJson.append("{\"text\":\"Account summary as of ").append(now).append("\"},");
            docJson.append("{\"table\":{\"name\":\"Summary\",");
            docJson.append("\"headers\":[\"Transactions\",\"Net Result\",\"Current Balance\"],");
            docJson.append("\"rows\":[\"").append(summaryRow).append("\"]}},");
            docJson.append("{\"table\":{\"name\":\"Transactions\",");
            docJson.append("\"headers\":[\"ID\",\"Type\",\"Amount\",\"Before\",\"After\",\"Timestamp\"],");
            docJson.append("\"rows\":[").append(formatRowsForJson(rowsSb.toString())).append("]}},");
            docJson.append("{\"footer\":\"Swarm Blackjack - bank-service - Generated ").append(now).append("\"}");
            docJson.append("]}");

            // Call document-service
            try {
                HttpClient client = HttpClient.newHttpClient();
                HttpRequest docReq = HttpRequest.newBuilder()
                    .uri(URI.create(documentServiceUrl + "/document"))
                    .header("Content-Type", "application/json")
                    .POST(HttpRequest.BodyPublishers.ofString(docJson.toString()))
                    .build();

                HttpResponse<byte[]> docRes = client.send(docReq, HttpResponse.BodyHandlers.ofByteArray());

                if (docRes.statusCode() != 200) {
                    log.severe("Document service error: " + docRes.statusCode());
                    sendJson(ex, 502, "{\"error\":\"document generation failed\"}"); return;
                }

                byte[] pdf = docRes.body();
                String filename = "transactions-" + now.substring(0, 10) + ".pdf";
                ex.getResponseHeaders().set("Content-Type", "application/pdf");
                ex.getResponseHeaders().set("Content-Disposition", "attachment; filename=\"" + filename + "\"");
                ex.getResponseHeaders().set("Access-Control-Allow-Origin", "*");
                ex.sendResponseHeaders(200, pdf.length);
                try (OutputStream os = ex.getResponseBody()) { os.write(pdf); }
                log.info(String.format("Export: player=%s txns=%d pdf=%d bytes", playerId, txCount, pdf.length));

            } catch (Exception e) {
                log.severe("Export call failed: " + e.getMessage());
                sendJson(ex, 502, "{\"error\":\"document service unavailable\"}");
            }
        }

        private static String formatRowsForJson(String rows) {
            if (rows == null || rows.isEmpty()) return "";
            StringBuilder sb = new StringBuilder();
            for (String row : rows.split("\n")) {
                if (sb.length() > 0) sb.append(",");
                sb.append("\"").append(row.replace("\\", "\\\\").replace("\"", "\\\"")).append("\"");
            }
            return sb.toString();
        }
    }

    // ── DEV ONLY ──────────────────────────────────────────────────────────────
    // ── DEV ONLY ──────────────────────────────────────────────────────────────

    static class DevResetHandler implements HttpHandler {
        @Override
        public void handle(HttpExchange ex) throws IOException {
            ex.getResponseHeaders().set("Access-Control-Allow-Origin", "*");
            ex.getResponseHeaders().set("Access-Control-Allow-Methods", "POST, OPTIONS");
            if ("OPTIONS".equals(ex.getRequestMethod())) { ex.sendResponseHeaders(204, -1); return; }
            if (!"POST".equals(ex.getRequestMethod())) {
                sendJson(ex, 405, "{\"error\":\"method not allowed\"}"); return;
            }
            try {
                Connection c = db.get();
                try (Statement st = c.createStatement()) {
                    st.execute("TRUNCATE TABLE open_bets, transactions, accounts RESTART IDENTITY CASCADE");
                }
                db.seedDemoPlayer();
                log.warning("DEV RESET: all accounts, transactions, and open bets wiped. Demo player re-seeded.");
                sendJson(ex, 200, "{\"reset\":true,\"service\":\"bank-service\"}");
            } catch (SQLException e) {
                log.severe("Dev reset error: " + e.getMessage());
                sendJson(ex, 500, "{\"error\":\"reset failed: " + e.getMessage() + "\"}");
            }
        }
    }
}
