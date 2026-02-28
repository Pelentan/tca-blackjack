package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// ── Data Types ────────────────────────────────────────────────────────────────

type Card struct {
	Suit string `json:"suit"`
	Rank string `json:"rank"`
}

type PlayerState struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Chips       int    `json:"chips"`
	CurrentBet  int    `json:"currentBet"`
	Hand        []Card `json:"hand"`
	HandValue   int    `json:"handValue"`
	IsSoftHand  bool   `json:"isSoftHand"`
	Status      string `json:"status"`
	BankTxID    string `json:"-"` // internal only — never sent to frontend
	BankTxID2   string `json:"-"` // double-down additional bet transaction
}

type DealerState struct {
	Hand        []Card `json:"hand"`
	HandValue   int    `json:"handValue"`
	IsRevealed  bool   `json:"isRevealed"`
}

type GameState struct {
	TableID        string        `json:"tableId"`
	Phase          string        `json:"phase"`
	Players        []PlayerState `json:"players"`
	Dealer         DealerState   `json:"dealer"`
	ActivePlayerID *string       `json:"activePlayerId"`
	MinBet         int           `json:"minBet"`
	MaxBet         int           `json:"maxBet"`
	HandledBy      string        `json:"handledBy"`
	Timestamp      string        `json:"timestamp"`
}

type SSEEvent struct {
	Type string    `json:"type"`
	Data GameState `json:"data"`
}

type PlayerActionRequest struct {
	PlayerID string `json:"playerId"`
	Action   string `json:"action"`
	Amount   int    `json:"amount,omitempty"`
}

// ── Table ─────────────────────────────────────────────────────────────────────

type Table struct {
	mu        sync.RWMutex
	state     GameState
	clients   map[chan GameState]struct{}
	isDemo    bool
	phase     int // cycling demo phases
}

func NewTable(tableID string) *Table {
	playerID := "player-00000000-0000-0000-0000-000000000001"

	// Seed starting balance — idempotent, bank ignores if player already exists
	startingChips := 1000
	mtlsClient.Post(bankServiceURL+"/account",
		"application/json",
		bytes.NewReader([]byte(fmt.Sprintf(
			`{"playerId":"%s","startingBalance":"%d.00"}`, playerID, startingChips,
		))),
	)

	// Read authoritative balance from bank
	if balance := callBankBalance(playerID); balance >= 0 {
		startingChips = balance
	}

	return &Table{
		isDemo:  true,
		clients: make(map[chan GameState]struct{}),
		state: GameState{
			TableID: tableID,
			Phase:   "waiting",
			Players: []PlayerState{
				{
					ID:     playerID,
					Name:   "Player 1",
					Chips:  startingChips,
					Hand:   []Card{},
					Status: "waiting",
				},
			},
			Dealer: DealerState{
				Hand:       []Card{},
				IsRevealed: false,
			},
			MinBet:    10,
			MaxBet:    500,
			HandledBy: hostname(),
			Timestamp: now(),
		},
	}
}

// NewPlayerTable creates an event-driven table for a real authenticated player.
// Bank calls are made BEFORE this is called — do not hold the registry lock here.
func NewPlayerTable(tableID, playerID, playerName string, startingChips int) *Table {
	return &Table{
		isDemo:  false,
		clients: make(map[chan GameState]struct{}),
		state: GameState{
			TableID: tableID,
			Phase:   "waiting",
			Players: []PlayerState{{
				ID:     playerID,
				Name:   playerName,
				Chips:  startingChips,
				Hand:   []Card{},
				Status: "waiting",
			}},
			Dealer:    DealerState{Hand: []Card{}, IsRevealed: false},
			MinBet:    10,
			MaxBet:    500,
			HandledBy: hostname(),
			Timestamp: now(),
		},
	}
}

func (t *Table) Subscribe() chan GameState {
	ch := make(chan GameState, 16)
	t.mu.Lock()
	t.clients[ch] = struct{}{}
	t.mu.Unlock()
	return ch
}

func (t *Table) Unsubscribe(ch chan GameState) {
	t.mu.Lock()
	delete(t.clients, ch)
	t.mu.Unlock()
	close(ch)
}

func (t *Table) Broadcast(state GameState) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for ch := range t.clients {
		select {
		case ch <- state:
		default:
		}
	}
}

func (t *Table) SetState(state GameState) {
	t.mu.Lock()
	t.state = state
	t.mu.Unlock()
	t.Broadcast(state)
}

func (t *Table) GetState() GameState {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.state
}

// ── Table Registry ─────────────────────────────────────────────────────────────

type Registry struct {
	mu     sync.RWMutex
	tables map[string]*Table
}

func NewRegistry() *Registry {
	return &Registry{tables: make(map[string]*Table)}
}

func (r *Registry) Get(id string) (*Table, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tables[id]
	return t, ok
}

func (r *Registry) GetOrCreate(id string) *Table {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.tables[id]; ok {
		return t
	}
	// Never auto-create player tables via GetOrCreate — they must be explicitly
	// created via CreatePlayerTable so the correct player ID and balance are set.
	// Return nil for player-table-* IDs that don't exist yet.
	if len(id) > 13 && id[:13] == "player-table-" {
		return nil
	}
	t := NewTable(id)
	r.tables[id] = t
	return t
}

func (r *Registry) List() []GameState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	states := make([]GameState, 0, len(r.tables))
	for _, t := range r.tables {
		states = append(states, t.GetState())
	}
	return states
}

// CreatePlayerTable creates or refreshes a player-owned table.
// Bank HTTP calls happen outside the registry lock to avoid blocking SSE connections.
func (r *Registry) CreatePlayerTable(playerID, playerName string) *Table {
	tableID := "player-table-" + playerID

	// Check if table already exists (read lock only)
	r.mu.RLock()
	existing, ok := r.tables[tableID]
	r.mu.RUnlock()

	if ok {
		// Refresh balance outside any lock
		if balance := callBankBalance(playerID); balance >= 0 {
			existing.mu.Lock()
			if len(existing.state.Players) > 0 {
				existing.state.Players[0].Chips = balance
				existing.state.Players[0].Name  = playerName
			}
			existing.mu.Unlock()
		}
		return existing
	}

	// New table — do bank calls before taking the registry lock
	mtlsClient.Post(bankServiceURL+"/account",
		"application/json",
		bytes.NewReader([]byte(fmt.Sprintf(
			`{"playerId":"%s","startingBalance":"1000.00"}`, playerID,
		))),
	)
	startingChips := 1000
	if balance := callBankBalance(playerID); balance >= 0 {
		startingChips = balance
	}

	t := NewPlayerTable(tableID, playerID, playerName, startingChips)

	// Now take the write lock just to insert
	r.mu.Lock()
	defer r.mu.Unlock()
	// Double-check in case of concurrent creation
	if existing, ok := r.tables[tableID]; ok {
		return existing
	}
	r.tables[tableID] = t
	return t
}

// ── Demo Game Loop ─────────────────────────────────────────────────────────────
// Cycles the default table through realistic game phases so the UI has
// something to render without real players. Calls stub services so the
// observability dashboard shows real inter-service traffic.

// demoPaused controls whether the demo loop runs. Toggle via POST /demo/pause.
var demoPaused int32 // atomic: 0=running, 1=paused

func runDemoLoop(table *Table) {
	phases := []func(*Table){
		phaseBetting,
		phaseDealing,
		phasePlayerTurn,
		phaseDealerTurn,
		phasePayout,
	}
	for {
		for _, phase := range phases {
			// Check pause between phases
			for atomic.LoadInt32(&demoPaused) == 1 {
				time.Sleep(500 * time.Millisecond)
			}
			phase(table)
		}
	}
}

func phaseBetting(t *Table) {
	log.Println("[demo] phase: betting")
	s := t.GetState()
	s.Phase = "betting"
	s.Dealer = DealerState{Hand: []Card{}, IsRevealed: false}

	betAmount := 50
	for i := range s.Players {
		s.Players[i].Hand = []Card{}
		s.Players[i].HandValue = 0
		s.Players[i].CurrentBet = betAmount
		s.Players[i].Status = "betting"

		// Auto-replenish broke demo player before attempting bet
		if s.Players[i].Chips < betAmount {
			log.Printf("[demo] player %s is broke — auto-replenishing 1000 chips", s.Players[i].ID)
			if newBal := callBankDeposit(s.Players[i].ID, 1000); newBal >= 0 {
				s.Players[i].Chips = newBal
				log.Printf("[demo] replenished: player=%s newBalance=%d", s.Players[i].ID, newBal)
			}
		}

		txID, newBalance := callBankBet(s.Players[i].ID, betAmount)
		if txID != "" {
			s.Players[i].BankTxID = txID
			s.Players[i].Chips = newBalance
			log.Printf("[bank] bet placed: player=%s amount=%d txId=%s balance=%d",
				s.Players[i].ID, betAmount, txID, newBalance)
		} else {
			log.Printf("[bank] bet failed for player=%s — skipping hand", s.Players[i].ID)
		}
	}

	s.HandledBy = hostname()
	s.Timestamp = now()
	t.SetState(s)

	// Show betting state long enough to read
	time.Sleep(1500 * time.Millisecond)
}

func phaseDealing(t *Table) {
	log.Println("[demo] phase: dealing — calling deck-service")

	// Fetch all 4 cards upfront — one service call, deal them out visually one by one
	cards := callDeckService(t.state.TableID, 4)
	if len(cards) < 4 {
		cards = defaultCards()
	}

	s := t.GetState()
	s.Phase = "dealing"
	s.Players[0].Status = "playing"
	s.Players[0].Hand = []Card{}
	s.Dealer = DealerState{Hand: []Card{}, IsRevealed: false}
	t.SetState(s)
	time.Sleep(400 * time.Millisecond)

	// Card 1: player first card
	s = t.GetState()
	s.Players[0].Hand = []Card{cards[0]}
	s.HandledBy = hostname()
	s.Timestamp = now()
	t.SetState(s)
	time.Sleep(600 * time.Millisecond)

	// Card 2: dealer face-up card
	s = t.GetState()
	s.Dealer.Hand = []Card{cards[2]}
	s.Dealer.HandValue = cardValue(cards[2])
	s.HandledBy = hostname()
	s.Timestamp = now()
	t.SetState(s)
	time.Sleep(600 * time.Millisecond)

	// Card 3: player second card
	s = t.GetState()
	s.Players[0].Hand = []Card{cards[0], cards[1]}
	handResult := callHandEvaluator(s.Players[0].Hand)
	s.Players[0].HandValue = handResult.Value
	s.Players[0].IsSoftHand = handResult.IsSoft
	s.HandledBy = hostname()
	s.Timestamp = now()
	t.SetState(s)
	time.Sleep(600 * time.Millisecond)

	// Card 4: dealer hole card (face down)
	s = t.GetState()
	s.Dealer.Hand = []Card{cards[2], {Suit: "hidden", Rank: "hidden"}}
	pid := s.Players[0].ID
	s.ActivePlayerID = &pid
	s.HandledBy = hostname()
	s.Timestamp = now()
	t.SetState(s)

	// Pause on the dealt hands before player turn
	time.Sleep(1200 * time.Millisecond)
}

func phasePlayerTurn(t *Table) {
	log.Println("[demo] phase: player_turn")
	s := t.GetState()
	s.Phase = "player_turn"
	s.HandledBy = hostname()
	s.Timestamp = now()
	t.SetState(s)

	// Brief pause — player "thinking"
	time.Sleep(1500 * time.Millisecond)

	// Demo: player hits once
	hitCards := callDeckService(s.TableID, 1)
	s = t.GetState()
	if len(hitCards) > 0 {
		s.Players[0].Hand = append(s.Players[0].Hand, hitCards[0])
	} else {
		s.Players[0].Hand = append(s.Players[0].Hand, Card{Suit: "hearts", Rank: "5"})
	}

	handResult := callHandEvaluator(s.Players[0].Hand)
	s.Players[0].HandValue = handResult.Value
	s.Players[0].IsSoftHand = handResult.IsSoft
	if handResult.IsBust {
		s.Players[0].Status = "bust"
	} else {
		s.Players[0].Status = "standing"
	}

	s.HandledBy = hostname()
	s.Timestamp = now()
	t.SetState(s)

	// Pause to show the final player hand
	time.Sleep(1200 * time.Millisecond)
}

func phaseDealerTurn(t *Table) {
	log.Println("[demo] phase: dealer_turn — calling dealer-ai")
	s := t.GetState()
	s.Phase = "dealer_turn"
	s.HandledBy = hostname()
	s.Timestamp = now()
	t.SetState(s)
	time.Sleep(600 * time.Millisecond)

	// Reveal hole card
	s = t.GetState()
	revealCards := callDeckService(s.TableID, 1)
	if len(revealCards) > 0 {
		s.Dealer.Hand[1] = revealCards[0]
	} else {
		s.Dealer.Hand[1] = Card{Suit: "clubs", Rank: "7"}
	}
	s.Dealer.IsRevealed = true

	handResult := callHandEvaluator(s.Dealer.Hand)
	s.Dealer.HandValue = handResult.Value
	s.HandledBy = hostname()
	s.Timestamp = now()
	t.SetState(s)
	time.Sleep(800 * time.Millisecond)

	// Ask dealer AI, then hit one card at a time until 17+
	for s.Dealer.HandValue < 17 {
		decision := callDealerAI(s.Dealer.Hand)
		log.Printf("[demo] dealer AI decision: %s (value=%d)", decision, s.Dealer.HandValue)

		hitCards := callDeckService(s.TableID, 1)
		s = t.GetState()
		if len(hitCards) > 0 {
			s.Dealer.Hand = append(s.Dealer.Hand, hitCards[0])
		} else {
			s.Dealer.Hand = append(s.Dealer.Hand, Card{Suit: "diamonds", Rank: "3"})
		}
		handResult = callHandEvaluator(s.Dealer.Hand)
		s.Dealer.HandValue = handResult.Value
		s.HandledBy = hostname()
		s.Timestamp = now()
		t.SetState(s)
		time.Sleep(700 * time.Millisecond)
	}

	s = t.GetState()
	s.ActivePlayerID = nil
	s.HandledBy = hostname()
	s.Timestamp = now()
	t.SetState(s)

	// Pause to show final dealer hand before payout
	time.Sleep(1000 * time.Millisecond)
}

func phasePayout(t *Table) {
	log.Println("[demo] phase: payout")
	s := t.GetState()
	s.Phase = "payout"

	playerVal := s.Players[0].HandValue
	dealerVal := s.Dealer.HandValue

	var outcome string
	// Natural blackjack: exactly 2 cards totalling 21 (dealer did not also have blackjack)
	playerBlackjack := playerVal == 21 && len(s.Players[0].Hand) == 2
	dealerBlackjack  := dealerVal == 21 && len(s.Dealer.Hand) == 2
	if s.Players[0].Status == "bust" {
		s.Players[0].Status = "lost"
		outcome = "loss"
	} else if playerBlackjack && dealerBlackjack {
		// Both have natural — push, no 3:2 bonus
		s.Players[0].Status = "push"
		outcome = "push"
	} else if playerBlackjack {
		s.Players[0].Status = "blackjack"
		outcome = "blackjack"
	} else if dealerVal > 21 || playerVal > dealerVal {
		s.Players[0].Status = "won"
		outcome = "win"
	} else if playerVal == dealerVal {
		s.Players[0].Status = "push"
		outcome = "push"
	} else {
		s.Players[0].Status = "lost"
		outcome = "loss"
	}

	// Settle with bank — bank owns the balance
	txID := s.Players[0].BankTxID
	if txID != "" {
		newBalance := callBankPayout(txID, outcome)
		if newBalance >= 0 {
			s.Players[0].Chips = newBalance
			log.Printf("[bank] payout settled: player=%s txId=%s result=%s balance=%d",
				s.Players[0].ID, txID, outcome, newBalance)
		} else {
			log.Printf("[bank] payout failed for txId=%s — balance may be stale", txID)
		}
		s.Players[0].BankTxID = ""
	} else {
		log.Printf("[bank] no txId for payout — bet may have failed earlier")
	}

	s.HandledBy = hostname()
	s.Timestamp = now()
	t.SetState(s)

	// Show the result — long enough to read win/loss and updated chips
	time.Sleep(2500 * time.Millisecond)

	// Reset to waiting — brief pause then next hand begins
	s = t.GetState()
	s.Phase = "waiting"
	s.Players[0].Status = "waiting"
	s.Players[0].CurrentBet = 0
	s.HandledBy = hostname()
	s.Timestamp = now()
	t.SetState(s)

	time.Sleep(800 * time.Millisecond)
}

// ── Player State Machine ─────────────────────────────────────────────────────
// All functions run in a goroutine — they may sleep for visual pacing.
// Table.SetState broadcasts each update to connected SSE clients.

func processPlayerAction(table *Table, action PlayerActionRequest) {
	s := table.GetState()
	// Use the table's actual player ID — guards against session/table ID mismatch
	if len(s.Players) > 0 {
		action.PlayerID = s.Players[0].ID
	}
	switch s.Phase {
	case "waiting":
		if action.Action == "bet" {
			playerBet(table, action)
		}
	case "player_turn":
		switch action.Action {
		case "hit":
			playerHit(table)
		case "stand":
			playerStand(table)
		case "double":
			playerDouble(table)
		case "split":
			// Stubbed — acknowledge but do nothing
			log.Println("[game-state] split: stubbed, action ignored")
		}
	}
}

func playerBet(table *Table, action PlayerActionRequest) {
	s := table.GetState()
	if len(s.Players) == 0 {
		return
	}
	amount := action.Amount
	if amount < s.MinBet {
		amount = s.MinBet
	}
	if amount > s.MaxBet {
		amount = s.MaxBet
	}
	if amount > s.Players[0].Chips {
		amount = s.Players[0].Chips
	}
	if amount <= 0 {
		return
	}

	txID, newBalance := callBankBet(s.Players[0].ID, amount)
	if txID == "" {
		log.Printf("[game-state] bet rejected by bank for player=%s", s.Players[0].ID)
		return
	}

	s.Players[0].BankTxID = txID
	s.Players[0].CurrentBet = amount
	s.Players[0].Chips = newBalance
	s.Players[0].Status = "betting"
	s.Players[0].Hand = []Card{}
	s.Players[0].HandValue = 0
	s.Dealer = DealerState{Hand: []Card{}, IsRevealed: false}
	s.Phase = "betting"
	s.HandledBy = hostname()
	s.Timestamp = now()
	table.SetState(s)
	time.Sleep(500 * time.Millisecond)

	// Initialize shoe for this table (idempotent — 409 if already exists is fine)
	initShoe(s.TableID)

	// Deal 4 cards: p1, dealer-up, p2, dealer-hole
	cards := callDeckService(s.TableID, 4)
	if len(cards) < 4 {
		cards = defaultCards()
	}

	s = table.GetState()
	s.Phase = "dealing"
	table.SetState(s)
	time.Sleep(300 * time.Millisecond)

	// Player card 1
	s = table.GetState()
	s.Players[0].Hand = []Card{cards[0]}
	hr := callHandEvaluator(s.Players[0].Hand)
	s.Players[0].HandValue = hr.Value
	s.HandledBy = hostname()
	s.Timestamp = now()
	table.SetState(s)
	time.Sleep(500 * time.Millisecond)

	// Dealer face-up
	s = table.GetState()
	s.Dealer.Hand = []Card{cards[1]}
	s.Dealer.HandValue = cardValue(cards[1])
	s.HandledBy = hostname()
	s.Timestamp = now()
	table.SetState(s)
	time.Sleep(500 * time.Millisecond)

	// Player card 2
	s = table.GetState()
	s.Players[0].Hand = append(s.Players[0].Hand, cards[2])
	hr = callHandEvaluator(s.Players[0].Hand)
	s.Players[0].HandValue = hr.Value
	s.Players[0].IsSoftHand = hr.IsSoft
	s.HandledBy = hostname()
	s.Timestamp = now()
	table.SetState(s)
	time.Sleep(500 * time.Millisecond)

	// Dealer hole card (hidden)
	s = table.GetState()
	s.Dealer.Hand = append(s.Dealer.Hand, Card{Suit: "hidden", Rank: "hidden"})
	pid := s.Players[0].ID
	s.ActivePlayerID = &pid
	s.HandledBy = hostname()
	s.Timestamp = now()
	table.SetState(s)
	time.Sleep(600 * time.Millisecond)

	// Natural blackjack check
	if hr.Value == 21 {
		s = table.GetState()
		s.Players[0].Status = "blackjack"
		s.Phase = "player_turn"
		s.HandledBy = hostname()
		s.Timestamp = now()
		table.SetState(s)
		time.Sleep(1000 * time.Millisecond)
		runDealerTurnPlayer(table)
		return
	}

	s = table.GetState()
	s.Phase = "player_turn"
	s.Players[0].Status = "playing"
	s.HandledBy = hostname()
	s.Timestamp = now()
	table.SetState(s)
}

func playerHit(table *Table) {
	s := table.GetState()
	if len(s.Players) == 0 || s.Players[0].Status != "playing" {
		return
	}
	cards := callDeckService(s.TableID, 1)
	if len(cards) > 0 {
		s.Players[0].Hand = append(s.Players[0].Hand, cards[0])
	} else {
		s.Players[0].Hand = append(s.Players[0].Hand, Card{Suit: "hearts", Rank: "7"})
	}
	hr := callHandEvaluator(s.Players[0].Hand)
	s.Players[0].HandValue = hr.Value
	s.Players[0].IsSoftHand = hr.IsSoft
	s.HandledBy = hostname()
	s.Timestamp = now()
	if hr.IsBust {
		s.Players[0].Status = "bust"
		s.ActivePlayerID = nil
		table.SetState(s)
		time.Sleep(800 * time.Millisecond)
		runDealerTurnPlayer(table)
		return
	}
	if hr.Value == 21 {
		s.Players[0].Status = "standing"
		s.ActivePlayerID = nil
		table.SetState(s)
		time.Sleep(600 * time.Millisecond)
		runDealerTurnPlayer(table)
		return
	}
	// Five-card Charlie: 5 cards without busting = automatic win
	if len(s.Players[0].Hand) >= 5 {
		log.Printf("[game] five-card charlie: player wins with %d cards, value=%d", len(s.Players[0].Hand), hr.Value)
		s.Players[0].Status = "standing"
		s.ActivePlayerID = nil
		table.SetState(s)
		time.Sleep(600 * time.Millisecond)
		runDealerTurnPlayer(table)
		return
	}
	table.SetState(s)
}

func playerStand(table *Table) {
	s := table.GetState()
	if len(s.Players) == 0 {
		return
	}
	s.Players[0].Status = "standing"
	s.ActivePlayerID = nil
	s.HandledBy = hostname()
	s.Timestamp = now()
	table.SetState(s)
	time.Sleep(400 * time.Millisecond)
	runDealerTurnPlayer(table)
}

func playerDouble(table *Table) {
	s := table.GetState()
	if len(s.Players) == 0 || s.Players[0].Status != "playing" {
		return
	}
	additionalBet := s.Players[0].CurrentBet
	if additionalBet > s.Players[0].Chips {
		// Can't afford full double — fall back to hit
		playerHit(table)
		return
	}
	txID2, newBalance := callBankBet(s.Players[0].ID, additionalBet)
	if txID2 == "" {
		playerHit(table)
		return
	}
	s = table.GetState()
	s.Players[0].CurrentBet += additionalBet
	s.Players[0].Chips = newBalance
	s.Players[0].BankTxID2 = txID2

	// One card, forced stand
	cards := callDeckService(s.TableID, 1)
	if len(cards) > 0 {
		s.Players[0].Hand = append(s.Players[0].Hand, cards[0])
	} else {
		s.Players[0].Hand = append(s.Players[0].Hand, Card{Suit: "diamonds", Rank: "4"})
	}
	hr := callHandEvaluator(s.Players[0].Hand)
	s.Players[0].HandValue = hr.Value
	s.Players[0].IsSoftHand = hr.IsSoft
	if hr.IsBust {
		s.Players[0].Status = "bust"
	} else {
		s.Players[0].Status = "standing"
	}
	s.ActivePlayerID = nil
	s.HandledBy = hostname()
	s.Timestamp = now()
	table.SetState(s)
	time.Sleep(600 * time.Millisecond)
	runDealerTurnPlayer(table)
}

func runDealerTurnPlayer(table *Table) {
	s := table.GetState()
	s.Phase = "dealer_turn"
	s.HandledBy = hostname()
	s.Timestamp = now()
	table.SetState(s)
	time.Sleep(600 * time.Millisecond)

	// Reveal hole card — draw from shoe
	s = table.GetState()
	if len(s.Dealer.Hand) >= 2 {
		realCards := callDeckService(s.TableID, 1)
		if len(realCards) > 0 {
			s.Dealer.Hand[1] = realCards[0]
		} else {
			s.Dealer.Hand[1] = Card{Suit: "clubs", Rank: "8"}
		}
	}
	s.Dealer.IsRevealed = true
	hr := callHandEvaluator(s.Dealer.Hand)
	s.Dealer.HandValue = hr.Value
	s.HandledBy = hostname()
	s.Timestamp = now()
	table.SetState(s)
	time.Sleep(800 * time.Millisecond)

	// If player busted, no need to play dealer hand
	s = table.GetState()
	playerBust := len(s.Players) > 0 && s.Players[0].Status == "bust"

	if !playerBust {
		for s.Dealer.HandValue < 17 {
			callDealerAI(s.Dealer.Hand)
			hitCards := callDeckService(s.TableID, 1)
			s = table.GetState()
			if len(hitCards) > 0 {
				s.Dealer.Hand = append(s.Dealer.Hand, hitCards[0])
			} else {
				s.Dealer.Hand = append(s.Dealer.Hand, Card{Suit: "spades", Rank: "3"})
			}
			hr = callHandEvaluator(s.Dealer.Hand)
			s.Dealer.HandValue = hr.Value
			s.HandledBy = hostname()
			s.Timestamp = now()
			table.SetState(s)
			time.Sleep(700 * time.Millisecond)
		}
	}

	runPayoutPlayer(table)
}

func runPayoutPlayer(table *Table) {
	s := table.GetState()
	s.Phase = "payout"

	if len(s.Players) == 0 {
		table.SetState(s)
		return
	}

	playerVal := s.Players[0].HandValue
	dealerVal := s.Dealer.HandValue
	playerStatus := s.Players[0].Status

	playerBlackjack  := playerVal == 21 && len(s.Players[0].Hand) == 2 && playerStatus == "blackjack"
	dealerBlackjack  := dealerVal == 21 && len(s.Dealer.Hand) == 2
	fiveCardCharlie  := len(s.Players[0].Hand) >= 5 && playerStatus != "bust"

	var outcome string
	switch {
	case playerStatus == "bust":
		s.Players[0].Status = "lost"
		outcome = "loss"
	case playerBlackjack && dealerBlackjack:
		s.Players[0].Status = "push"
		outcome = "push"
	case playerBlackjack:
		s.Players[0].Status = "blackjack"
		outcome = "blackjack"
	case fiveCardCharlie:
		// Five-card Charlie beats dealer regardless of dealer total
		s.Players[0].Status = "won"
		outcome = "win"
	case dealerVal > 21 || playerVal > dealerVal:
		s.Players[0].Status = "won"
		outcome = "win"
	case playerVal == dealerVal:
		s.Players[0].Status = "push"
		outcome = "push"
	default:
		s.Players[0].Status = "lost"
		outcome = "loss"
	}

	// Settle primary bet
	if txID := s.Players[0].BankTxID; txID != "" {
		if newBalance := callBankPayout(txID, outcome); newBalance >= 0 {
			s.Players[0].Chips = newBalance
		}
		s.Players[0].BankTxID = ""
	}
	// Settle double-down additional bet
	if txID2 := s.Players[0].BankTxID2; txID2 != "" {
		if newBalance := callBankPayout(txID2, outcome); newBalance >= 0 {
			s.Players[0].Chips = newBalance
		}
		s.Players[0].BankTxID2 = ""
	}

	s.HandledBy = hostname()
	s.Timestamp = now()
	table.SetState(s)
	time.Sleep(2500 * time.Millisecond)

	// Reset to waiting for next hand
	s = table.GetState()
	s.Phase = "waiting"
	s.Players[0].Status = "waiting"
	s.Players[0].CurrentBet = 0
	s.Players[0].Hand = []Card{}
	s.Players[0].HandValue = 0
	s.Players[0].IsSoftHand = false
	s.Dealer = DealerState{Hand: []Card{}, IsRevealed: false}
	s.ActivePlayerID = nil
	s.HandledBy = hostname()
	s.Timestamp = now()
	table.SetState(s)
}

func initShoe(tableID string) {
	body, _ := json.Marshal(map[string]interface{}{
		"tableId":   tableID,
		"deckCount": 6,
	})
	resp, err := http.Post(deckServiceURL+"/shoe", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[deck-service] initShoe error: %v", err)
		return
	}
	resp.Body.Close()
	// 409 = shoe already exists, that's fine
}

// ── Upstream Service Calls ─────────────────────────────────────────────────────

var (
	deckServiceURL     = getEnv("DECK_SERVICE_URL", "https://deck-service:3002")
	handEvaluatorURL   = getEnv("HAND_EVALUATOR_URL", "https://hand-evaluator:3003")
	dealerAIURL        = getEnv("DEALER_AI_URL", "https://dealer-ai:3004")
	observabilityURL   = getEnv("OBSERVABILITY_URL", "http://observability-service:3009")
	bankServiceURL     = getEnv("BANK_SERVICE_URL", "https://bank-service:3005")
)

// mtlsClient is used for calls to mTLS-enabled services (game domain).
// Loaded at startup with the service cert and CA. Falls back to default
// http.DefaultClient if TLS env vars are not present.
var mtlsClient *http.Client

func initMTLSClient() {
	certFile := getEnv("TLS_CERT", "")
	keyFile  := getEnv("TLS_KEY", "")
	caFile   := getEnv("TLS_CA", "")

	if certFile == "" || keyFile == "" || caFile == "" {
		log.Println("[tls] no cert env vars — using plain HTTP client for game domain calls")
		mtlsClient = &http.Client{Timeout: 5 * time.Second}
		return
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		log.Fatalf("[tls] failed to load client cert: %v", err)
	}

	caCert, err := os.ReadFile(caFile)
	if err != nil {
		log.Fatalf("[tls] failed to read CA cert: %v", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caCert) {
		log.Fatal("[tls] failed to parse CA cert")
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS12,
	}

	mtlsClient = &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}
	log.Println("[tls] mTLS client ready — game domain calls use mutual TLS")
}

// reportEvent fires a non-blocking event report to the observability service.
// Fire and forget — never blocks game logic.
func reportEvent(callee, method, path string, status int, latencyMs int64) {
	go func() {
		body, _ := json.Marshal(map[string]interface{}{
			"caller":      "game-state",
			"callee":      callee,
			"method":      method,
			"path":        path,
			"status_code": status,
			"latency_ms":  latencyMs,
			"protocol":    "http",
		})
		resp, err := http.Post(observabilityURL+"/event", "application/json", bytes.NewReader(body))
		if err != nil {
			log.Printf("[observability] report error: %v", err)
			return
		}
		resp.Body.Close()
	}()
}

type DeckDealResponse struct {
	Cards []Card `json:"cards"`
}

func callDeckService(tableID string, count int) []Card {
	body, _ := json.Marshal(map[string]int{"count": count})
	start := time.Now()
	path := fmt.Sprintf("/shoe/%s/deal", tableID)
	resp, err := mtlsClient.Post(
		fmt.Sprintf("%s%s", deckServiceURL, path),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		log.Printf("[deck-service] error: %v", err)
		reportEvent("deck-service", "POST", path, 503, time.Since(start).Milliseconds())
		return nil
	}
	defer resp.Body.Close()
	reportEvent("deck-service", "POST", path, resp.StatusCode, time.Since(start).Milliseconds())
	var result DeckDealResponse
	json.NewDecoder(resp.Body).Decode(&result)
	return result.Cards
}

type HandResult struct {
	Value      int  `json:"value"`
	IsSoft     bool `json:"isSoft"`
	IsBlackjack bool `json:"isBlackjack"`
	IsBust     bool `json:"isBust"`
}

func callHandEvaluator(hand []Card) HandResult {
	body, _ := json.Marshal(map[string]interface{}{"cards": hand})
	start := time.Now()
	resp, err := mtlsClient.Post(
		fmt.Sprintf("%s/evaluate", handEvaluatorURL),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		log.Printf("[hand-evaluator] error: %v", err)
		reportEvent("hand-evaluator", "POST", "/evaluate", 503, time.Since(start).Milliseconds())
		return HandResult{Value: estimateValue(hand)}
	}
	defer resp.Body.Close()
	reportEvent("hand-evaluator", "POST", "/evaluate", resp.StatusCode, time.Since(start).Milliseconds())
	var result HandResult
	json.NewDecoder(resp.Body).Decode(&result)
	return result
}

func callDealerAI(hand []Card) string {
	body, _ := json.Marshal(map[string]interface{}{"hand": hand})
	start := time.Now()
	resp, err := mtlsClient.Post(
		fmt.Sprintf("%s/decide", dealerAIURL),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		log.Printf("[dealer-ai] error: %v", err)
		reportEvent("dealer-ai", "POST", "/decide", 503, time.Since(start).Milliseconds())
		return "stand"
	}
	defer resp.Body.Close()
	reportEvent("dealer-ai", "POST", "/decide", resp.StatusCode, time.Since(start).Milliseconds())
	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	return result["action"]
}

// ── Bank Service Calls ────────────────────────────────────────────────────────

type BetResponse struct {
	TransactionID string `json:"transactionId"`
	NewBalance    string `json:"newBalance"` // bank returns string e.g. "975.00"
}

type PayoutResponse struct {
	NewBalance string `json:"newBalance"` // bank returns string e.g. "975.00"
}

// callBankBet deducts the bet from the player's bank balance.
// Returns transaction_id to be held until payout, and new balance.
func callBankBet(playerID string, amount int) (string, int) {
	start := time.Now()
	body, _ := json.Marshal(map[string]string{
		"playerId": playerID,
		"amount":   fmt.Sprintf("%d.00", amount),
	})
	resp, err := mtlsClient.Post(bankServiceURL+"/bet", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[bank-service] bet error: %v", err)
		reportEvent("bank-service", "POST", "/bet", 503, time.Since(start).Milliseconds())
		return "", -1
	}
	defer resp.Body.Close()
	reportEvent("bank-service", "POST", "/bet", resp.StatusCode, time.Since(start).Milliseconds())

	if resp.StatusCode != 200 {
		log.Printf("[bank-service] bet rejected: status=%d", resp.StatusCode)
		return "", -1
	}

	var result BetResponse
	json.NewDecoder(resp.Body).Decode(&result)
	var bal float64
	fmt.Sscanf(result.NewBalance, "%f", &bal)
	return result.TransactionID, int(bal)
}

// callBankPayout settles a bet transaction.
// result must be "win", "loss", or "push".
// Returns new balance after settlement.
func callBankPayout(txID string, result string) int {
	start := time.Now()
	body, _ := json.Marshal(map[string]string{
		"transactionId": txID,
		"result":        result,
	})
	resp, err := mtlsClient.Post(bankServiceURL+"/payout", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[bank-service] payout error: %v", err)
		reportEvent("bank-service", "POST", "/payout", 503, time.Since(start).Milliseconds())
		return -1
	}
	defer resp.Body.Close()
	reportEvent("bank-service", "POST", "/payout", resp.StatusCode, time.Since(start).Milliseconds())

	if resp.StatusCode != 200 {
		log.Printf("[bank-service] payout rejected: status=%d", resp.StatusCode)
		return -1
	}

	var pr PayoutResponse
	json.NewDecoder(resp.Body).Decode(&pr)
	var bal float64
	fmt.Sscanf(pr.NewBalance, "%f", &bal)
	return int(bal)
}

// callBankDeposit tops up a player's account. Used by demo loop when balance hits zero.
func callBankDeposit(playerID string, amount int) int {
	start := time.Now()
	body, _ := json.Marshal(map[string]string{
		"playerId": playerID,
		"amount":   fmt.Sprintf("%d.00", amount),
		"note":     "demo auto-replenish",
	})
	resp, err := mtlsClient.Post(bankServiceURL+"/deposit", "application/json", bytes.NewReader(body))
	if err != nil {
		reportEvent("bank-service", "POST", "/deposit", 503, time.Since(start).Milliseconds())
		log.Printf("[bank] deposit error: %v", err)
		return -1
	}
	defer resp.Body.Close()
	reportEvent("bank-service", "POST", "/deposit", resp.StatusCode, time.Since(start).Milliseconds())
	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	var bal float64
	fmt.Sscanf(result["newBalance"], "%f", &bal)
	return int(bal)
}

// callBankBalance fetches current balance for display on startup/reconnect.
func callBankBalance(playerID string) int {
	start := time.Now()
	resp, err := http.Get(fmt.Sprintf("%s/balance?playerId=%s", bankServiceURL, playerID))
	if err != nil {
		reportEvent("bank-service", "GET", "/balance", 503, time.Since(start).Milliseconds())
		return -1
	}
	defer resp.Body.Close()
	reportEvent("bank-service", "GET", "/balance", resp.StatusCode, time.Since(start).Milliseconds())

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if b, ok := result["balance"].(float64); ok {
		return int(b)
	}
	return -1
}

// ── HTTP Handlers ─────────────────────────────────────────────────────────────

func main() {
	// Init mTLS client before anything that makes outbound calls
	initMTLSClient()

	registry := NewRegistry()

	// Create and start demo table
	demoTableID := "demo-table-00000000-0000-0000-0000-000000000001"
	demoTable := registry.GetOrCreate(demoTableID)
	go runDemoLoop(demoTable)

	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "healthy",
			"service": "game-state",
		})
	})

	mux.HandleFunc("/tables", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(registry.List())
	})

	// POST /tables/create — create a player-owned table
	// Reached via gateway rewrite: /api/game/create → /tables/create
	mux.HandleFunc("/tables/create", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			PlayerID   string `json:"playerId"`
			PlayerName string `json:"playerName"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PlayerID == "" {
			http.Error(w, `{"error":"playerId required"}`, http.StatusBadRequest)
			return
		}
		if req.PlayerName == "" {
			req.PlayerName = "Player"
		}
		table := registry.CreatePlayerTable(req.PlayerID, req.PlayerName)
		s := table.GetState()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tableId":  s.TableID,
			"phase":    s.Phase,
			"playerId": req.PlayerID,
		})
	})

	// GET /tables/{id} - state snapshot
	// GET /tables/{id}/stream - SSE
	// POST /tables/{id}/action - player action
	mux.HandleFunc("/tables/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// /tables/{id}/stream
		if len(path) > 8 && path[len(path)-7:] == "/stream" {
			tableID := path[8 : len(path)-7]
			sseHandler(w, r, registry, tableID)
			return
		}

		// /tables/{id}/action
		if len(path) > 8 && path[len(path)-7:] == "/action" {
			tableID := path[8 : len(path)-7]
			actionHandler(w, r, registry, tableID)
			return
		}

		// /tables/{id}/join
		if len(path) > 8 && path[len(path)-5:] == "/join" {
			tableID := path[8 : len(path)-5]
			table := registry.GetOrCreate(tableID)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(table.GetState())
			return
		}

		// /tables/{id}
		tableID := path[8:]
		if tableID == "" {
			http.NotFound(w, r)
			return
		}
		table, ok := registry.Get(tableID)
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(table.GetState())
	})

	// POST /demo/pause — toggle demo loop on/off
	mux.HandleFunc("/demo/pause", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if atomic.LoadInt32(&demoPaused) == 0 {
			atomic.StoreInt32(&demoPaused, 1)
			log.Println("[demo] paused")
		} else {
			atomic.StoreInt32(&demoPaused, 0)
			log.Println("[demo] resumed")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"paused": atomic.LoadInt32(&demoPaused) == 1,
		})
	})

	port     := getEnv("PORT", "3001")
	certFile := getEnv("TLS_CERT", "")
	keyFile  := getEnv("TLS_KEY", "")
	caFile   := getEnv("TLS_CA", "")

	if certFile == "" || keyFile == "" || caFile == "" {
		log.Printf("🃏 Game State service starting on :%s (plaintext — no TLS env vars)", port)
		log.Printf("   Demo table: %s", demoTableID)
		if err := http.ListenAndServe(":"+port, corsMiddleware(mux)); err != nil {
			log.Fatal(err)
		}
		return
	}

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
		Handler:   corsMiddleware(mux),
		TLSConfig: tlsCfg,
	}

	log.Printf("🃏 Game State service starting on :%s (mTLS)", port)
	log.Printf("   Demo table: %s", demoTableID)
	if err := srv.ListenAndServeTLS(certFile, keyFile); err != nil {
		log.Fatal(err)
	}
}

func sseHandler(w http.ResponseWriter, r *http.Request, registry *Registry, tableID string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	table := registry.GetOrCreate(tableID)
	if table == nil {
		http.Error(w, "table not found", http.StatusNotFound)
		return
	}
	ch := table.Subscribe()
	defer table.Unsubscribe(ch)

	// Send current state immediately on connect
	sendSSEEvent(w, flusher, "game_state", table.GetState())

	for {
		select {
		case state, ok := <-ch:
			if !ok {
				return
			}
			sendSSEEvent(w, flusher, "game_state", state)
		case <-r.Context().Done():
			return
		}
	}
}

func sendSSEEvent(w http.ResponseWriter, flusher http.Flusher, eventType string, state GameState) {
	evt := SSEEvent{Type: eventType, Data: state}
	data, _ := json.Marshal(evt)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data)
	flusher.Flush()
}

func actionHandler(w http.ResponseWriter, r *http.Request, registry *Registry, tableID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var action PlayerActionRequest
	if err := json.NewDecoder(r.Body).Decode(&action); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	table, ok := registry.Get(tableID)
	if !ok {
		http.NotFound(w, r)
		return
	}

	// Demo table: acknowledge but do not process
	if table.isDemo {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"accepted": true,
			"message":  "demo table — actions are simulated",
		})
		return
	}

	// Validate action is legal for current phase
	s := table.GetState()
	valid := false
	switch s.Phase {
	case "waiting":
		valid = action.Action == "bet"
	case "player_turn":
		valid = action.Action == "hit" || action.Action == "stand" ||
			action.Action == "double" || action.Action == "split"
	}
	if !valid {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"accepted": false,
			"message":  fmt.Sprintf("action '%s' not valid in phase '%s'", action.Action, s.Phase),
		})
		return
	}

	// Respond 202 immediately, process async
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{"accepted": true, "message": "processing"})

	go processPlayerAction(table, action)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "game-state"
	}
	return h
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func defaultCards() []Card {
	suits := []string{"hearts", "diamonds", "clubs", "spades"}
	ranks := []string{"A", "2", "3", "4", "5", "6", "7", "8", "9", "10", "J", "Q", "K"}
	cards := make([]Card, 4)
	for i := range cards {
		cards[i] = Card{
			Suit: suits[rand.Intn(len(suits))],
			Rank: ranks[rand.Intn(len(ranks))],
		}
	}
	return cards
}

func cardValue(c Card) int {
	switch c.Rank {
	case "A":
		return 11
	case "J", "Q", "K", "10":
		return 10
	default:
		v := 0
		fmt.Sscanf(c.Rank, "%d", &v)
		return v
	}
}

func estimateValue(hand []Card) int {
	total := 0
	aces := 0
	for _, c := range hand {
		switch c.Rank {
		case "A":
			aces++
			total += 11
		case "J", "Q", "K", "10":
			total += 10
		default:
			v := 0
			fmt.Sscanf(c.Rank, "%d", &v)
			total += v
		}
	}
	for aces > 0 && total > 21 {
		total -= 10
		aces--
	}
	return total
}
