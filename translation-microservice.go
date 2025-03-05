package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"cloud.google.com/go/translate"
	"github.com/go-redis/redis/v8"
	"golang.org/x/oauth2/google"
	"golang.org/x/text/language"
	"google.golang.org/api/option"
)

// TranslationRequest represents the incoming request for translation
type TranslationRequest struct {
	Text       string `json:"text"`
	SourceLang string `json:"source_lang,omitempty"` // ISO 639-1 code, optional
	TargetLang string `json:"target_lang"`           // ISO 639-1 code, required
	AuthToken  string `json:"auth_token"`            // Authentication token
}

// TranslationResponse represents the response from the translation service
type TranslationResponse struct {
	TranslatedText string `json:"translated_text"`
	SourceLang     string `json:"source_lang"`
	TargetLang     string `json:"target_lang"`
	CacheHit       bool   `json:"cache_hit"`
}

// Configuration for the service
type Config struct {
	RedisAddress  string
	RedisPassword string
	RedisDB       int
	ServerPort    string
	TTL           time.Duration
	AuthToken     string // Authentication token to validate requests
}

// Global clients
var (
	redisClient     *redis.Client
	translateClient *translate.Client
	config          Config
)

func init() {
	// Set up configuration
	config = Config{
		RedisAddress:  getEnv("REDIS_ADDRESS", "localhost:6379"),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),
		RedisDB:       0, // Using default DB
		ServerPort:    getEnv("SERVER_PORT", "8080"),
		TTL:           time.Hour * 24 * 14, // 2 weeks TTL
		AuthToken:     getEnv("AUTH_TOKEN", ""),
	}

	// Print Redis connection details to help with debugging
	log.Printf("Attempting to connect to Redis/Valkey at: %s", config.RedisAddress)

	// redisClient = nil
	if os.Getenv("USE_REDIS_UNSECURE") != "" {
		// Set up Redis client with options specific to AWS Valkey compatibility
		redisClient = redis.NewClient(&redis.Options{
			Addr:     config.RedisAddress,
			Password: config.RedisPassword,
			DB:       config.RedisDB,
		})
	} else {
		// Set up Redis client with TLS
		redisClient = redis.NewClient(&redis.Options{
			Addr:     config.RedisAddress,
			Password: config.RedisPassword,
			DB:       config.RedisDB,
			TLSConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				// For production, you should verify the Redis server's certificate
				// InsecureSkipVerify: false,
			},
		})
	}

	// Test Redis connection - with retry logic to handle initial connectivity issues
	ctx := context.Background()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	log.Println("Connected to Redis successfully")

	// Set up Google Translate client
	var err error
	if credJSON := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_JSON"); credJSON != "" {
		// Print the first few characters for debugging (avoid printing the whole credential)
		log.Printf("Credentials string found (first 20 chars): %s...", credJSON[:min(20, len(credJSON))])

		// Try to parse JSON to verify its structure
		var jsonMap map[string]interface{}
		if err := json.Unmarshal([]byte(credJSON), &jsonMap); err != nil {
			log.Fatalf("Invalid JSON format in credentials: %v", err)
		}

		ctx := context.Background()
		creds, credErr := google.CredentialsFromJSON(ctx, []byte(credJSON),
			"https://www.googleapis.com/auth/cloud-platform")
		if credErr != nil {
			log.Fatalf("Failed to create credentials: %v", credErr)
		}
		translateClient, err = translate.NewClient(ctx, option.WithCredentials(creds))
		if err != nil {
			log.Fatalf("Failed to create translate client: %v", err)
		}
		log.Println("Connected to Google Translate API using credentials from environment variable")
	} else {
		// Fall back to GOOGLE_APPLICATION_CREDENTIALS file
		translateClient, err = translate.NewClient(ctx)
		if err != nil {
			log.Fatalf("Failed to create translate client: %v", err)
		}
		log.Println("Connected to Google Translate API using credentials from file")
	}
}

func main() {
	// Set up HTTP routes
	http.HandleFunc("/translate", handleTranslation)
	http.HandleFunc("/health", handleHealth)

	// Start server
	log.Printf("Translation service started on port %s", config.ServerPort)
	if err := http.ListenAndServe(":"+config.ServerPort, nil); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}

// handleHealth provides a simple health check endpoint
func handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check Redis connection
	ctx := r.Context()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		http.Error(w, fmt.Sprintf("Redis health check failed: %v", err), http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// authenticateRequest validates the authentication token
func authenticateRequest(token string) bool {
	// Compare the provided token with the configured token
	return token == config.AuthToken
}

// handleTranslation processes translation requests
func handleTranslation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request
	var req TranslationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	// Authenticate request
	if !authenticateRequest(req.AuthToken) {
		http.Error(w, "Unauthorized: Invalid authentication token", http.StatusUnauthorized)
		log.Printf("Unauthorized request attempt with token: %s", req.AuthToken)
		return
	}

	// Validate request
	if req.Text == "" {
		http.Error(w, "Text field is required", http.StatusBadRequest)
		return
	}
	if req.TargetLang == "" {
		http.Error(w, "Target language is required", http.StatusBadRequest)
		return
	}

	// Process translation
	ctx := r.Context()
	response, err := translateText(ctx, req)
	if err != nil {
		http.Error(w, fmt.Sprintf("Translation failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Return response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// translateText handles the translation with caching
func translateText(ctx context.Context, req TranslationRequest) (*TranslationResponse, error) {
	// Create cache key
	cacheKey := fmt.Sprintf("translate:%s:%s:%s", req.SourceLang, req.TargetLang, req.Text)

	// Check if Redis is available before attempting to use cache
	if redisClient != nil {
		// Check cache first
		cachedResult, err := redisClient.Get(ctx, cacheKey).Result()
		if err == nil {
			// Cache hit
			var response TranslationResponse
			if err := json.Unmarshal([]byte(cachedResult), &response); err != nil {
				return nil, fmt.Errorf("failed to unmarshal cached result: %v", err)
			}
			response.CacheHit = true
			return &response, nil
		} else if err != redis.Nil {
			// Redis error - log but continue with translation
			log.Printf("Redis error when checking cache: %v", err)
		}
	}

	// Cache miss or Redis unavailable, perform translation
	var sourceLang language.Tag
	if req.SourceLang != "" {
		var err error
		sourceLang, err = language.Parse(req.SourceLang)
		if err != nil {
			return nil, fmt.Errorf("invalid source language: %v", err)
		}
	}

	targetLang, err := language.Parse(req.TargetLang)
	if err != nil {
		return nil, fmt.Errorf("invalid target language: %v", err)
	}

	var translations []translate.Translation
	var detectedSourceLang string

	opts := &translate.Options{
		Format: translate.Text,
	}

	if req.SourceLang != "" {
		// Source language is specified
		translations, err = translateClient.Translate(ctx, []string{req.Text}, targetLang, &translate.Options{
			Source: sourceLang,
			Format: translate.Text,
		})
		detectedSourceLang = req.SourceLang
	} else {
		// Auto-detect source language
		translations, err = translateClient.Translate(ctx, []string{req.Text}, targetLang, opts)
		if err == nil && len(translations) > 0 {
			detectedSourceLang = translations[0].Source.String()
		}
	}

	if err != nil {
		return nil, fmt.Errorf("translation API error: %v", err)
	}

	if len(translations) == 0 {
		return nil, fmt.Errorf("no translation returned")
	}

	// Create response
	response := &TranslationResponse{
		TranslatedText: translations[0].Text,
		SourceLang:     detectedSourceLang,
		TargetLang:     req.TargetLang,
		CacheHit:       false,
	}

	// Cache the result if Redis is available
	if redisClient != nil {
		jsonData, err := json.Marshal(response)
		if err != nil {
			log.Printf("Warning: Failed to marshal response for caching: %v", err)
		} else {
			if err := redisClient.Set(ctx, cacheKey, jsonData, config.TTL).Err(); err != nil {
				log.Printf("Warning: Failed to cache translation: %v", err)
			}
		}
	}

	return response, nil
}

// getEnv gets an environment variable or returns a default value
func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
