package main

import (
	"crypto/tls"
	"crypto/x509"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/redis/go-redis/v9"
)

func getEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func main() {
	log.SetFlags(log.Ltime | log.Lshortfile)
	log.Printf("[bank] starting — Go + GnuCOBOL bank service")

	// ── Config ────────────────────────────────────────────────────────────────
	port := getEnv("PORT", "3005")
	cobolDir = getEnv("COBOL_BIN_DIR", "/usr/local/bin/cobol")

	dbHost := getEnv("BANK_DB_HOST", "bank-db")
	dbPort := getEnv("BANK_DB_PORT", "5432")
	dbName := getEnv("BANK_DB_NAME", "bankdb")
	dbUser := getEnv("BANK_DB_USER", "bankuser")
	dbPass := getEnv("BANK_DB_PASSWORD", "bankpass")

	redisHost := getEnv("REDIS_HOST", "redis")
	redisPort := getEnv("REDIS_PORT", "6379")

	documentServiceURL = getEnv("DOCUMENT_SERVICE_URL", "http://document-service:3011")

	// ── Database ──────────────────────────────────────────────────────────────
	db, err := NewDB(dbHost, dbPort, dbName, dbUser, dbPass)
	if err != nil {
		log.Fatalf("[bank] database: %v", err)
	}
	if err := db.Migrate(); err != nil {
		log.Fatalf("[bank] migrate: %v", err)
	}
	if err := db.SeedDemoPlayer(); err != nil {
		log.Fatalf("[bank] seed: %v", err)
	}

	// ── Redis (optional — balance pub/sub) ────────────────────────────────────
	var rdb *redis.Client
	rdb = redis.NewClient(&redis.Options{
		Addr: redisHost + ":" + redisPort,
	})
	log.Printf("[bank] Redis configured at %s:%s", redisHost, redisPort)

	// ── Routes ────────────────────────────────────────────────────────────────
	mux := http.NewServeMux()

	mux.HandleFunc("/health",        healthHandler(db))
	mux.HandleFunc("/account",       accountHandler(db))
	mux.HandleFunc("/balance",       balanceHandler(db))
	mux.HandleFunc("/transactions",  transactionsHandler(db))
	mux.HandleFunc("/bet",           betHandler(db, rdb))
	mux.HandleFunc("/payout",        payoutHandler(db, rdb))
	mux.HandleFunc("/deposit",       depositHandler(db, rdb))
	mux.HandleFunc("/withdraw",      withdrawHandler(db, rdb))
	mux.HandleFunc("/export",        exportHandler(db))
	mux.HandleFunc("/dev/reset",     devResetHandler(db))

	certFile := getEnv("TLS_CERT", "")
	keyFile  := getEnv("TLS_KEY", "")
	caFile   := getEnv("TLS_CA", "")

	if certFile == "" || keyFile == "" || caFile == "" {
		log.Printf("[bank] listening on :%s (plaintext — no TLS env vars)", port)
		if err := http.ListenAndServe(":"+port, mux); err != nil {
			log.Fatalf("[bank] server: %v", err)
		}
		return
	}

	caCert, err := os.ReadFile(caFile)
	if err != nil {
		log.Fatalf("[bank][tls] failed to read CA cert: %v", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caCert) {
		log.Fatal("[bank][tls] failed to parse CA cert")
	}

	tlsCfg := &tls.Config{
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  caPool,
		MinVersion: tls.VersionTLS12,
	}

	srv := &http.Server{
		Addr:      ":" + port,
		Handler:   mux,
		TLSConfig: tlsCfg,
	}

	log.Printf("[bank] listening on :%s (mTLS)", port)
	if err := srv.ListenAndServeTLS(certFile, keyFile); err != nil {
		log.Fatalf("[bank] server: %v", err)
	}
}
