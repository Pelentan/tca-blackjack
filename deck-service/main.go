package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"log"
	"math/rand"
	"net/http"
	"os"
	"sync"
)

type Card struct {
	Suit string `json:"suit"`
	Rank string `json:"rank"`
}

type Shoe struct {
	Cards     []Card
	TableID   string
	DeckCount int
}

var (
	shoes   = make(map[string]*Shoe)
	shoesMu sync.RWMutex

	suits = []string{"hearts", "diamonds", "clubs", "spades"}
	ranks = []string{"A", "2", "3", "4", "5", "6", "7", "8", "9", "10", "J", "Q", "K"}
)

func newShoe(tableID string, deckCount int) *Shoe {
	cards := make([]Card, 0, 52*deckCount)
	for d := 0; d < deckCount; d++ {
		for _, s := range suits {
			for _, r := range ranks {
				cards = append(cards, Card{Suit: s, Rank: r})
			}
		}
	}
	rand.Shuffle(len(cards), func(i, j int) { cards[i], cards[j] = cards[j], cards[i] })
	return &Shoe{Cards: cards, TableID: tableID, DeckCount: deckCount}
}

func getOrCreateShoe(tableID string) *Shoe {
	shoesMu.Lock()
	defer shoesMu.Unlock()
	if shoe, ok := shoes[tableID]; ok {
		return shoe
	}
	shoe := newShoe(tableID, 6)
	shoes[tableID] = shoe
	return shoe
}

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "healthy", "service": "deck-service", "language": "Go"})
	})

	// POST /shoe/{tableId}/deal
	mux.HandleFunc("/shoe/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		// Extract tableId from path
		path := r.URL.Path // /shoe/{tableId}/deal or /shoe/{tableId}
		if r.Method == http.MethodPost && len(path) > 6 {
			// POST /shoe/{tableId}/deal
			var req struct {
				Count int `json:"count"`
			}
			req.Count = 1
			json.NewDecoder(r.Body).Decode(&req)

			tableID := extractTableID(path)
			shoe := getOrCreateShoe(tableID)

			shoesMu.Lock()
			dealt := make([]Card, 0, req.Count)
			for i := 0; i < req.Count && len(shoe.Cards) > 0; i++ {
				dealt = append(dealt, shoe.Cards[0])
				shoe.Cards = shoe.Cards[1:]
			}
			remaining := len(shoe.Cards)
			shoesMu.Unlock()

			log.Printf("[deck-service] dealt %d cards to table %s (%d remaining)", len(dealt), tableID, remaining)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"cards": dealt,
				"shoeStatus": map[string]interface{}{
					"tableId":        tableID,
					"remainingCards": remaining,
					"deckCount":      shoe.DeckCount,
				},
			})
			return
		}
		http.NotFound(w, r)
	})

	port    := getEnv("PORT", "3002")
	certFile := getEnv("TLS_CERT", "")
	keyFile  := getEnv("TLS_KEY", "")
	caFile   := getEnv("TLS_CA", "")

	if certFile == "" || keyFile == "" || caFile == "" {
		log.Printf("🃏 Deck Service (Go) starting on :%s (plaintext — no TLS env vars)", port)
		if err := http.ListenAndServe(":"+port, mux); err != nil {
			log.Fatal(err)
		}
		return
	}

	// Load CA cert pool for client verification
	caCert, err := os.ReadFile(caFile)
	if err != nil {
		log.Fatalf("[tls] failed to read CA cert: %v", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caCert) {
		log.Fatal("[tls] failed to parse CA cert")
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

	log.Printf("🃏 Deck Service (Go) starting on :%s (mTLS)", port)
	if err := srv.ListenAndServeTLS(certFile, keyFile); err != nil {
		log.Fatal(err)
	}
}

func extractTableID(path string) string {
	// /shoe/{tableId}/deal  or  /shoe/{tableId}
	parts := []rune(path[6:]) // strip /shoe/
	for i, ch := range parts {
		if ch == '/' {
			return string(parts[:i])
		}
	}
	return string(parts)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
