package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/redis/go-redis/v9"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func parseBody(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

func queryParam(q url.Values, key string) string {
	return strings.TrimSpace(q.Get(key))
}

// publishBalance publishes a balance update to Redis.
// Fire-and-forget: errors are logged but not returned.
func publishBalance(rdb *redis.Client, playerID, balance string) {
	if rdb == nil {
		return
	}
	payload := fmt.Sprintf(`{"playerId":"%s","balance":%s}`, playerID, balance)
	if err := rdb.Publish(context.Background(), "tca:balance", payload).Err(); err != nil {
		log.Printf("[bank] Redis publish failed (non-fatal): %v", err)
	}
}

// ── Health ────────────────────────────────────────────────────────────────────

func healthHandler(db *DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, 405, "method_not_allowed", "GET only")
			return
		}
		writeJSON(w, 200, map[string]string{
			"status":   "healthy",
			"service":  "bank-service",
			"language": "Go + COBOL (GnuCOBOL)",
		})
	}
}

// ── Account ───────────────────────────────────────────────────────────────────

func accountHandler(db *DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, 405, "method_not_allowed", "POST only")
			return
		}
		var req struct {
			PlayerID        string `json:"playerId"`
			StartingBalance string `json:"startingBalance"`
		}
		if err := parseBody(r, &req); err != nil {
			writeError(w, 400, "bad_request", "invalid JSON")
			return
		}
		if req.PlayerID == "" {
			writeError(w, 400, "missing_field", "playerId required")
			return
		}
		starting := req.StartingBalance
		if starting == "" {
			starting = StartingBalance
		}
		exists, err := db.AccountExists(req.PlayerID)
		if err != nil {
			log.Printf("[bank] account exists check: %v", err)
			writeError(w, 500, "db_error", "database error")
			return
		}
		if exists {
			writeError(w, 409, "already_exists", "account already exists")
			return
		}
		if err := db.CreateAccount(req.PlayerID, starting); err != nil {
			log.Printf("[bank] create account: %v", err)
			writeError(w, 500, "db_error", "database error")
			return
		}
		writeJSON(w, 201, map[string]string{
			"playerId": req.PlayerID,
			"balance":  starting,
		})
	}
}

// ── Balance ───────────────────────────────────────────────────────────────────

func balanceHandler(db *DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		if r.Method != http.MethodGet {
			writeError(w, 405, "method_not_allowed", "GET only")
			return
		}
		playerID := queryParam(r.URL.Query(), "playerId")
		if playerID == "" {
			writeError(w, 400, "missing_param", "playerId required")
			return
		}
		balance, found, err := db.GetBalance(playerID)
		if err != nil {
			log.Printf("[bank] get balance: %v", err)
			writeError(w, 500, "db_error", "database error")
			return
		}
		if !found {
			writeError(w, 404, "not_found", "player account not found")
			return
		}
		writeJSON(w, 200, map[string]string{
			"playerId": playerID,
			"balance":  balance,
		})
	}
}

// ── Transactions ──────────────────────────────────────────────────────────────

func transactionsHandler(db *DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		if r.Method != http.MethodGet {
			writeError(w, 405, "method_not_allowed", "GET only")
			return
		}
		playerID := queryParam(r.URL.Query(), "playerId")
		if playerID == "" {
			writeError(w, 400, "missing_param", "playerId required")
			return
		}
		limit := 50
		txns, err := db.GetTransactions(playerID, limit)
		if err != nil {
			log.Printf("[bank] get transactions: %v", err)
			writeError(w, 500, "db_error", "database error")
			return
		}
		writeJSON(w, 200, map[string]any{
			"playerId":     playerID,
			"transactions": txns,
		})
	}
}

// ── Bet ───────────────────────────────────────────────────────────────────────

func betHandler(db *DB, rdb *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, 405, "method_not_allowed", "POST only")
			return
		}
		var req struct {
			PlayerID string `json:"playerId"`
			Amount   string `json:"amount"`
		}
		if err := parseBody(r, &req); err != nil {
			writeError(w, 400, "bad_request", "invalid JSON")
			return
		}
		if req.PlayerID == "" || req.Amount == "" {
			writeError(w, 400, "missing_field", "playerId and amount required")
			return
		}

		betCents, err := DollarsToCents(req.Amount)
		if err != nil || betCents <= 0 {
			writeError(w, 400, "invalid_amount", "amount must be a positive decimal")
			return
		}

		balanceStr, found, err := db.GetBalance(req.PlayerID)
		if err != nil {
			log.Printf("[bank] bet get balance: %v", err)
			writeError(w, 500, "db_error", "database error")
			return
		}
		if !found {
			writeError(w, 404, "not_found", "player account not found")
			return
		}

		balanceCents, err := DollarsToCents(balanceStr)
		if err != nil {
			log.Printf("[bank] bet parse balance %q: %v", balanceStr, err)
			writeError(w, 500, "internal_error", "balance format error")
			return
		}

		// COBOL: validate sufficient funds
		debit, err := ValidateDebit(balanceCents, betCents)
		if err != nil {
			log.Printf("[bank] COBOL validate-debit: %v", err)
			writeError(w, 500, "cobol_error", "bet validation failed")
			return
		}

		if debit.Status == "INSUFFICIENT" {
			// Demo player auto-replenish
			if req.PlayerID == DemoPlayerID {
				newBalStr, err := db.ReplenishDemoPlayer()
				if err != nil {
					log.Printf("[bank] demo replenish: %v", err)
					writeError(w, 500, "db_error", "replenish failed")
					return
				}
				// Re-run COBOL with fresh balance
				freshCents, _ := DollarsToCents(newBalStr)
				debit, err = ValidateDebit(freshCents, betCents)
				if err != nil || debit.Status == "INSUFFICIENT" {
					writeError(w, 409, "insufficient_funds", "bet exceeds maximum balance")
					return
				}
				balanceStr = newBalStr
				balanceCents = freshCents
			} else {
				writeJSON(w, 409, map[string]any{
					"error":     "insufficient_funds",
					"balance":   balanceStr,
					"requested": req.Amount,
				})
				return
			}
		}

		newBalStr := CentsToDollars(debit.NewBalanceCents)

		txID, err := db.PlaceBet(req.PlayerID, balanceStr, newBalStr, req.Amount)
		if err != nil {
			log.Printf("[bank] place bet: %v", err)
			writeError(w, 500, "db_error", "bet placement failed")
			return
		}

		log.Printf("[bank] bet: player=%s amount=%s txId=%s newBalance=%s",
			req.PlayerID, req.Amount, txID, newBalStr)

		writeJSON(w, 200, map[string]string{
			"transactionId": txID,
			"playerId":      req.PlayerID,
			"amount":        req.Amount,
			"newBalance":    newBalStr,
		})
	}
}

// ── Payout ────────────────────────────────────────────────────────────────────

func payoutHandler(db *DB, rdb *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, 405, "method_not_allowed", "POST only")
			return
		}
		var req struct {
			TransactionID string `json:"transactionId"`
			Result        string `json:"result"`
		}
		if err := parseBody(r, &req); err != nil {
			writeError(w, 400, "bad_request", "invalid JSON")
			return
		}
		if req.TransactionID == "" || req.Result == "" {
			writeError(w, 400, "missing_field", "transactionId and result required")
			return
		}

		bet, err := db.GetOpenBet(req.TransactionID)
		if err != nil {
			log.Printf("[bank] payout get open bet: %v", err)
			writeError(w, 500, "db_error", "database error")
			return
		}
		if bet == nil {
			writeError(w, 404, "not_found", "transaction not found or already settled")
			return
		}

		betCents, err := DollarsToCents(bet.Amount)
		if err != nil {
			log.Printf("[bank] payout parse bet amount: %v", err)
			writeError(w, 500, "internal_error", "bet amount format error")
			return
		}

		// COBOL: calculate payout amount
		payout, err := CalcPayout(betCents, req.Result)
		if err != nil {
			log.Printf("[bank] COBOL calc-payout: %v", err)
			writeError(w, 400, "invalid_result", err.Error())
			return
		}

		balanceStr, found, err := db.GetBalance(bet.PlayerID)
		if err != nil || !found {
			log.Printf("[bank] payout get balance: %v (found=%v)", err, found)
			writeError(w, 500, "db_error", "database error")
			return
		}

		balanceCents, err := DollarsToCents(balanceStr)
		if err != nil {
			writeError(w, 500, "internal_error", "balance format error")
			return
		}

		// COBOL: credit payout to balance
		newBalCents, err := CalcCredit(balanceCents, payout.ReturnedCents)
		if err != nil {
			log.Printf("[bank] COBOL calc-credit: %v", err)
			writeError(w, 500, "cobol_error", "credit calculation failed")
			return
		}

		returnedStr := CentsToDollars(payout.ReturnedCents)
		newBalStr := CentsToDollars(newBalCents)

		if err := db.SettlePayout(
			req.TransactionID, bet.PlayerID,
			balanceStr, newBalStr,
			returnedStr, payout.PayoutType,
		); err != nil {
			log.Printf("[bank] settle payout: %v", err)
			writeError(w, 500, "db_error", "payout settlement failed")
			return
		}

		log.Printf("[bank] payout: player=%s txId=%s result=%s returned=%s newBalance=%s",
			bet.PlayerID, req.TransactionID, req.Result, returnedStr, newBalStr)

		// Publish balance update to Redis for real-time UI
		publishBalance(rdb, bet.PlayerID, newBalStr)

		writeJSON(w, 200, map[string]string{
			"transactionId": req.TransactionID,
			"playerId":      bet.PlayerID,
			"result":        req.Result,
			"betAmount":     bet.Amount,
			"returned":      returnedStr,
			"newBalance":    newBalStr,
		})
	}
}

// ── Deposit ───────────────────────────────────────────────────────────────────

func depositHandler(db *DB, rdb *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, 405, "method_not_allowed", "POST only")
			return
		}
		var req struct {
			PlayerID string `json:"playerId"`
			Amount   string `json:"amount"`
			Note     string `json:"note"`
		}
		if err := parseBody(r, &req); err != nil {
			writeError(w, 400, "bad_request", "invalid JSON")
			return
		}
		if req.PlayerID == "" || req.Amount == "" {
			writeError(w, 400, "missing_field", "playerId and amount required")
			return
		}
		depositCents, err := DollarsToCents(req.Amount)
		if err != nil || depositCents <= 0 {
			writeError(w, 400, "invalid_amount", "amount must be positive")
			return
		}

		balanceStr, found, err := db.GetBalance(req.PlayerID)
		if err != nil || !found {
			writeError(w, 404, "not_found", "player account not found")
			return
		}

		balanceCents, _ := DollarsToCents(balanceStr)
		newBalCents, err := CalcCredit(balanceCents, depositCents)
		if err != nil {
			writeError(w, 500, "cobol_error", "deposit calculation failed")
			return
		}
		newBalStr := CentsToDollars(newBalCents)

		if err := db.ApplyBalanceChange(req.PlayerID, balanceStr, newBalStr, req.Amount, "deposit", req.Note); err != nil {
			writeError(w, 500, "db_error", "deposit failed")
			return
		}

		publishBalance(rdb, req.PlayerID, newBalStr)
		writeJSON(w, 200, map[string]string{"playerId": req.PlayerID, "newBalance": newBalStr})
	}
}

// ── Withdraw ──────────────────────────────────────────────────────────────────

func withdrawHandler(db *DB, rdb *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, 405, "method_not_allowed", "POST only")
			return
		}
		var req struct {
			PlayerID string `json:"playerId"`
			Amount   string `json:"amount"`
			Note     string `json:"note"`
		}
		if err := parseBody(r, &req); err != nil {
			writeError(w, 400, "bad_request", "invalid JSON")
			return
		}
		if req.PlayerID == "" || req.Amount == "" {
			writeError(w, 400, "missing_field", "playerId and amount required")
			return
		}
		withdrawCents, err := DollarsToCents(req.Amount)
		if err != nil || withdrawCents <= 0 {
			writeError(w, 400, "invalid_amount", "amount must be positive")
			return
		}

		balanceStr, found, err := db.GetBalance(req.PlayerID)
		if err != nil || !found {
			writeError(w, 404, "not_found", "player account not found")
			return
		}

		balanceCents, _ := DollarsToCents(balanceStr)
		debit, err := ValidateDebit(balanceCents, withdrawCents)
		if err != nil {
			writeError(w, 500, "cobol_error", "withdrawal validation failed")
			return
		}
		if debit.Status == "INSUFFICIENT" {
			writeJSON(w, 409, map[string]any{
				"error":     "insufficient_funds",
				"balance":   balanceStr,
				"requested": req.Amount,
			})
			return
		}

		newBalStr := CentsToDollars(debit.NewBalanceCents)
		if err := db.ApplyBalanceChange(req.PlayerID, balanceStr, newBalStr, req.Amount, "withdrawal", req.Note); err != nil {
			writeError(w, 500, "db_error", "withdrawal failed")
			return
		}

		publishBalance(rdb, req.PlayerID, newBalStr)
		writeJSON(w, 200, map[string]string{"playerId": req.PlayerID, "newBalance": newBalStr})
	}
}

// ── Export (PDF via document-service) ────────────────────────────────────────

var documentServiceURL = "http://document-service:3011"

func exportHandler(db *DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		if r.Method != http.MethodGet {
			writeError(w, 405, "method_not_allowed", "GET only")
			return
		}
		playerID := queryParam(r.URL.Query(), "playerId")
		if playerID == "" {
			writeError(w, 400, "missing_param", "playerId required")
			return
		}

		txns, err := db.GetTransactions(playerID, 200)
		if err != nil {
			writeError(w, 500, "db_error", "failed to fetch transactions")
			return
		}

		// Build document request
		rows := make([]string, 0, len(txns))
		for _, t := range txns {
			// CSV row: ID(short),Type,Amount,Balance After,Time
			id := t.ID
			if len(id) > 8 {
				id = id[:8]
			}
			rows = append(rows, fmt.Sprintf("%s,%s,%s,%s,%s",
				id, t.Type, t.Amount, t.BalanceAfter, t.CreatedAt))
		}

		docReq := map[string]any{
			"caller":  "bank-service",
			"title":   "Transaction History",
			"heading": fmt.Sprintf("Bank Transactions — Player %s", playerID),
			"blocks": []any{
				map[string]any{
					"table": map[string]any{
						"name":    "Transactions",
						"headers": []string{"ID", "Type", "Amount", "Balance After", "Timestamp"},
						"rows":    rows,
					},
				},
				map[string]any{"footer": "Generated by TCA Blackjack · bank-service (Go + COBOL)"},
			},
		}

		body, _ := json.Marshal(docReq)
		resp, err := http.Post(documentServiceURL+"/document", "application/json", strings.NewReader(string(body)))
		if err != nil {
			writeError(w, 502, "upstream_error", "document service unavailable")
			return
		}
		defer resp.Body.Close()

		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="transactions.pdf"`)
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}
}

// ── Dev reset ─────────────────────────────────────────────────────────────────

func devResetHandler(db *DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, 405, "method_not_allowed", "POST only")
			return
		}
		if err := db.DevReset(); err != nil {
			log.Printf("[bank] dev reset: %v", err)
			writeError(w, 500, "db_error", "reset failed")
			return
		}
		log.Printf("[bank] DEV RESET: all accounts wiped, demo player re-seeded")
		writeJSON(w, 200, map[string]bool{"reset": true})
	}
}

// ── CORS ──────────────────────────────────────────────────────────────────────

func setCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}
