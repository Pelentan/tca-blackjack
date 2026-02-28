// Auth UI Service v0.2.0
// ===============
// Language : Go
// Container: Scratch (static binary, no shell)
//
// Owns the auth form flow. Frontend-facing.
// Returns field definitions so the modal is server-driven.
// Orchestrates calls to auth-service — browser never touches auth-service directly.
//
// Endpoints:
//   GET  /fields?action=register|login  — field definitions for modal
//   POST /submit                        — validate + forward to auth-service
//   POST /passkey/register/begin        — proxy to auth-service (requires JWT)
//   POST /passkey/register/complete     — proxy to auth-service (requires JWT)
//   POST /passkey/login/begin           — proxy to auth-service
//   POST /passkey/login/complete        — proxy to auth-service
//   GET  /health

package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

var (
	authServiceURL = getEnv("AUTH_SERVICE_URL", "https://auth-service:3006")
	port           = getEnv("PORT", "3010")
	mtlsClient     *http.Client
)

func initMTLSClient() {
	certFile := getEnv("TLS_CERT", "")
	keyFile  := getEnv("TLS_KEY", "")
	caFile   := getEnv("TLS_CA", "")

	if certFile == "" || keyFile == "" || caFile == "" {
		mtlsClient = &http.Client{Timeout: 10 * time.Second}
		log.Println("[auth-ui] no TLS env vars — plain HTTP client")
		return
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		log.Fatalf("[auth-ui][tls] failed to load client cert: %v", err)
	}
	caCert, err := os.ReadFile(caFile)
	if err != nil {
		log.Fatalf("[auth-ui][tls] failed to read CA: %v", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caCert) {
		log.Fatal("[auth-ui][tls] failed to parse CA cert")
	}
	mtlsClient = &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cert},
				RootCAs:      caPool,
				MinVersion:   tls.VersionTLS12,
			},
		},
	}
	log.Println("[auth-ui][tls] mTLS client ready")
}

// ── Field definitions ─────────────────────────────────────────────────────────

type Field struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Placeholder string `json:"placeholder"`
	MaxLength   int    `json:"maxLength,omitempty"`
}

type FieldsResponse struct {
	Action string  `json:"action"`
	Title  string  `json:"title"`
	Submit string  `json:"submit"`
	Fields []Field `json:"fields"`
}

var registerFields = FieldsResponse{
	Action: "register",
	Title:  "Create Account",
	Submit: "Register",
	Fields: []Field{
		{Name: "name", Label: "Display Name", Type: "text", Required: true,
			Placeholder: "How should we call you?", MaxLength: 50},
		{Name: "email", Label: "Email Address", Type: "email", Required: true,
			Placeholder: "you@example.com", MaxLength: 255},
	},
}

var loginFields = FieldsResponse{
	Action: "login",
	Title:  "Sign In",
	Submit: "Sign In with Passkey",
	Fields: []Field{
		{Name: "email", Label: "Email Address", Type: "email", Required: true,
			Placeholder: "you@example.com", MaxLength: 255},
	},
}

// ── Validation ────────────────────────────────────────────────────────────────

var emailRegex = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

type SubmitRequest struct {
	Action string            `json:"action"`
	Fields map[string]string `json:"fields"`
}

type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func validateRegister(fields map[string]string) []ValidationError {
	var errs []ValidationError
	name := strings.TrimSpace(fields["name"])
	if name == "" {
		errs = append(errs, ValidationError{"name", "Display name is required"})
	} else if len(name) > 50 {
		errs = append(errs, ValidationError{"name", "Display name must be 50 characters or less"})
	}
	email := strings.TrimSpace(fields["email"])
	if email == "" {
		errs = append(errs, ValidationError{"email", "Email address is required"})
	} else if !emailRegex.MatchString(email) {
		errs = append(errs, ValidationError{"email", "Please enter a valid email address"})
	}
	return errs
}

func validateLogin(fields map[string]string) []ValidationError {
	var errs []ValidationError
	email := strings.TrimSpace(fields["email"])
	if email == "" {
		errs = append(errs, ValidationError{"email", "Email address is required"})
	} else if !emailRegex.MatchString(email) {
		errs = append(errs, ValidationError{"email", "Please enter a valid email address"})
	}
	return errs
}

// ── Auth service forwarding ───────────────────────────────────────────────────

type AuthResult struct {
	AccessToken string `json:"accessToken"`
	ExpiresIn   int    `json:"expiresIn"`
	PlayerID    string `json:"playerId"`
	PlayerName  string `json:"playerName"`
	Email       string `json:"email"`
}

func forwardToAuth(action string, fields map[string]string) (*AuthResult, int, string) {
	var endpoint string
	var payload map[string]string

	switch action {
	case "register":
		endpoint = "/register"
		payload = map[string]string{
			"email": strings.TrimSpace(strings.ToLower(fields["email"])),
			"name":  strings.TrimSpace(fields["name"]),
		}
	default:
		return nil, 400, "unknown action"
	}

	body, _ := json.Marshal(payload)
	start := time.Now()
	resp, err := mtlsClient.Post(authServiceURL+endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[auth-ui] auth-service unreachable (%dms): %v", time.Since(start).Milliseconds(), err)
		return nil, 503, "authentication service unavailable"
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	log.Printf("[auth-ui] auth-service %s → %d (%dms)", endpoint, resp.StatusCode, time.Since(start).Milliseconds())

	if resp.StatusCode == 409 {
		return nil, 409, "that email address is already registered"
	}
	if resp.StatusCode == 401 {
		return nil, 401, "email not found — have you registered?"
	}
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		var errResp map[string]string
		json.Unmarshal(respBody, &errResp)
		msg := errResp["error"]
		if msg == "" {
			msg = "authentication failed"
		}
		return nil, resp.StatusCode, msg
	}

	var result AuthResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, 500, "unexpected response from auth service"
	}
	return &result, resp.StatusCode, ""
}

// proxyToAuth transparently forwards a request to auth-service, preserving
// the Authorization header and body. Used for passkey ceremony endpoints.
func proxyToAuth(w http.ResponseWriter, r *http.Request, authPath string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "failed to read request body"})
		return
	}

	req, err := http.NewRequest(r.Method, authServiceURL+authPath, bytes.NewReader(body))
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "failed to create upstream request"})
		return
	}

	req.Header.Set("Content-Type", "application/json")
	if auth := r.Header.Get("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	}

	start := time.Now()
	resp, err := mtlsClient.Do(req)
	if err != nil {
		log.Printf("[auth-ui] auth-service unreachable at %s (%dms): %v", authPath, time.Since(start).Milliseconds(), err)
		writeJSON(w, 503, map[string]string{"error": "authentication service unavailable"})
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	log.Printf("[auth-ui] proxy %s → %d (%dms)", authPath, resp.StatusCode, time.Since(start).Milliseconds())

	corsHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func corsHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	corsHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func fieldsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsHeaders(w)
		w.WriteHeader(204)
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	switch r.URL.Query().Get("action") {
	case "register":
		writeJSON(w, 200, registerFields)
	case "login":
		writeJSON(w, 200, loginFields)
	default:
		writeJSON(w, 400, map[string]string{"error": "action must be 'register' or 'login'"})
	}
}

func submitHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsHeaders(w)
		w.WriteHeader(204)
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "method not allowed"})
		return
	}

	var req SubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.Fields == nil {
		req.Fields = map[string]string{}
	}

	var validationErrs []ValidationError
	switch req.Action {
	case "register":
		validationErrs = validateRegister(req.Fields)
	case "login":
		// Login is passkey-only — submit should not be called for login
		// (UI uses passkey ceremony directly). Return 400 to surface misconfiguration.
		writeJSON(w, 400, map[string]string{"error": "login requires passkey ceremony — use /passkey/login/begin"})
		return
	default:
		writeJSON(w, 400, map[string]string{"error": "action must be 'register' or 'login'"})
		return
	}

	if len(validationErrs) > 0 {
		writeJSON(w, 422, map[string]any{"error": "validation failed", "fields": validationErrs})
		return
	}

	result, status, errMsg := forwardToAuth(req.Action, req.Fields)
	if errMsg != "" {
		writeJSON(w, status, map[string]string{"error": errMsg})
		return
	}

	writeJSON(w, status, map[string]any{
		"accessToken": result.AccessToken,
		"expiresIn":   result.ExpiresIn,
		"playerId":    result.PlayerID,
		"playerName":  result.PlayerName,
		"email":       result.Email,
	})
}

// passkeyHandler routes /passkey/* to the appropriate auth-service endpoint
func passkeyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsHeaders(w)
		w.WriteHeader(204)
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "method not allowed"})
		return
	}

	// Strip /passkey prefix — auth-service uses the same path structure
	authPath := "/passkey" + strings.TrimPrefix(r.URL.Path, "/passkey")
	proxyToAuth(w, r, authPath)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	authStatus := "unreachable"
	resp, err := mtlsClient.Get(authServiceURL + "/health")
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode == 200 {
			authStatus = "reachable"
		}
	}
	writeJSON(w, 200, map[string]any{
		"status":       "healthy",
		"service":      "auth-ui-service",
		"language":     "Go",
		"container":    "scratch",
		"auth_service": authStatus,
		"actions":      []string{"register", "login", "passkey/register", "passkey/login"},
	})
}

// ── Main ──────────────────────────────────────────────────────────────────────

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	initMTLSClient()

	mux := http.NewServeMux()
	mux.HandleFunc("/fields", fieldsHandler)
	mux.HandleFunc("/submit", submitHandler)
	mux.HandleFunc("/passkey/", passkeyHandler)
	mux.HandleFunc("/health", healthHandler)

	log.Printf("[auth-ui-service] starting on :%s", port)
	log.Printf("[auth-ui-service] auth-service: %s", authServiceURL)

	if err := http.ListenAndServe(fmt.Sprintf(":%s", port), mux); err != nil {
		log.Fatal(err)
	}
}
