package credentials

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/secrets"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
	"github.com/windoze95/cantinarr-server/internal/trakt"
)

// Credential keys stored in the settings table.
const (
	KeyTMDBAccessToken = "tmdb_access_token"
	KeyAnthropicKey    = "anthropic_key"
	KeyOpenAIKey       = "openai_key"
	KeyGeminiKey       = "gemini_key"
	KeyGrokKey         = "grok_key"
	KeyTraktClientID   = "trakt_client_id"

	KeyAIProvider = "ai_provider"
	KeyAIModel    = "ai_model"
	// KeyAIHealthCheckEnabled controls only the scheduled shared-model probe.
	// Provider/model saves always perform their own validation turn.
	KeyAIHealthCheckEnabled = "ai_health_check_enabled"
	KeyAIHealthLastCheckAt  = "ai_health_last_check_at"
	// KeyOpenAIBaseURL points the shared openai provider at an
	// OpenAI-compatible endpoint. It is a plain setting, deliberately not in
	// AllKeys: AllKeys drives encryption at rest, the startup
	// EncryptExisting migration, per-key GET booleans, and DELETE, none of
	// which apply to a non-secret URL.
	KeyOpenAIBaseURL = "openai_base_url"
	// KeyOpenAIReasoningEffort pins the reasoning_effort the shared openai
	// provider sends on every turn. Empty means auto: interactive chat sends
	// no effort field and validation keeps its adaptive ladder. Same
	// plain-setting rationale as KeyOpenAIBaseURL.
	KeyOpenAIReasoningEffort = "openai_reasoning_effort"
)

// AIReasoningEfforts is the closed set of admin-settable shared openai
// reasoning efforts, lowest first. "none" maps to think-free turns on
// OpenAI-compatible servers (llama.cpp, vLLM, Ollama translate it), the rest
// trade latency for deliberation.
var AIReasoningEfforts = []string{"none", "minimal", "low", "medium", "high"}

// IsValidAIReasoningEffort reports whether value may be stored under
// KeyOpenAIReasoningEffort. Empty is valid and means auto.
func IsValidAIReasoningEffort(value string) bool {
	if value == "" {
		return true
	}
	for _, effort := range AIReasoningEfforts {
		if value == effort {
			return true
		}
	}
	return false
}

// AllKeys lists every credential key the system manages. Values for these
// keys are encrypted at rest; other settings (e.g. tool toggles) stay plain.
var AllKeys = []string{KeyTMDBAccessToken, KeyAnthropicKey, KeyOpenAIKey, KeyGeminiKey, KeyGrokKey, KeyTraktClientID}

const (
	AIProviderAnthropic = "anthropic"
	AIProviderOpenAI    = "openai"
	AIProviderGemini    = "gemini"
	AIProviderGrok      = "grok"
	AIProviderCodex     = "codex"
	AIProviderGrokOAuth = "grok_oauth"

	AIAuthTypeAPIKey    = "api_key"
	AIAuthTypeUserOAuth = "user_oauth"

	// DefaultAIProvider is the zero-config choice for untouched installs:
	// OAuth needs a ChatGPT login, not a purchased API key.
	DefaultAIProvider = AIProviderCodex

	// DefaultSharedAIModel pairs with that zero-config default: the fast
	// GPT-5.6 tier, so an untouched install doesn't spend the admin's ChatGPT
	// meter on heavyweight turns nobody chose.
	DefaultSharedAIModel = "gpt-5.6-luna"

	// AIHealthCheckInterval deliberately keeps the default background cost to
	// one tiny shared-provider turn per day.
	AIHealthCheckInterval = 24 * time.Hour
)

// AIModelOption describes one selectable chat model for the admin UI.
type AIModelOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// AIProviderOption describes a supported AI provider and its default models.
type AIProviderOption struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	AuthType      string `json:"auth_type"`
	CredentialKey string `json:"credential_key"`
	// SupportsBaseURL marks providers whose shared profile accepts an
	// admin-set endpoint override (openai only). The app renders the base
	// URL field from this flag, so servers without it never show the field.
	SupportsBaseURL bool `json:"supports_base_url,omitempty"`
	// SupportsReasoningEffort marks providers whose shared profile accepts a
	// pinned reasoning effort (openai only), with the same app-side gating:
	// no flag, no field.
	SupportsReasoningEffort bool            `json:"supports_reasoning_effort,omitempty"`
	Models                  []AIModelOption `json:"models"`
}

// AIConfig is the active provider/model pair used by the AI assistant.
type AIConfig struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

var AIProviders = []AIProviderOption{
	{
		ID:            AIProviderAnthropic,
		Label:         "Anthropic",
		AuthType:      AIAuthTypeAPIKey,
		CredentialKey: KeyAnthropicKey,
		Models: []AIModelOption{
			{ID: "claude-opus-4-8", Label: "Claude Opus 4.8", Description: "Most capable Claude Opus-tier model"},
			{ID: "claude-fable-5", Label: "Claude Fable 5", Description: "Highest-capability Claude model"},
			{ID: "claude-sonnet-5", Label: "Claude Sonnet 5", Description: "Latest balanced Claude model"},
			{ID: "claude-sonnet-4-6", Label: "Claude Sonnet 4.6", Description: "Balanced speed and intelligence"},
			{ID: "claude-haiku-4-5", Label: "Claude Haiku 4.5", Description: "Fastest, lowest-cost Claude option"},
		},
	},
	{
		ID:                      AIProviderOpenAI,
		Label:                   "OpenAI",
		AuthType:                AIAuthTypeAPIKey,
		CredentialKey:           KeyOpenAIKey,
		SupportsBaseURL:         true,
		SupportsReasoningEffort: true,
		Models: []AIModelOption{
			{ID: "gpt-5.5", Label: "GPT-5.5", Description: "Flagship OpenAI model"},
			{ID: "gpt-5.4", Label: "GPT-5.4", Description: "Affordable frontier model"},
			{ID: "gpt-5.4-mini", Label: "GPT-5.4 mini", Description: "Lower latency and cost"},
			{ID: "gpt-5.4-nano", Label: "GPT-5.4 nano", Description: "Smallest current GPT-5.4 model"},
			{ID: "gpt-4.1", Label: "GPT-4.1", Description: "Stable previous-generation model"},
			{ID: "gpt-4.1-mini", Label: "GPT-4.1 mini", Description: "Fast previous-generation model"},
		},
	},
	{
		ID:            AIProviderGemini,
		Label:         "Google Gemini",
		AuthType:      AIAuthTypeAPIKey,
		CredentialKey: KeyGeminiKey,
		Models: []AIModelOption{
			{ID: "gemini-3.5-flash", Label: "Gemini 3.5 Flash", Description: "Current stable Gemini Flash model"},
			{ID: "gemini-3.1-flash-lite", Label: "Gemini 3.1 Flash-Lite", Description: "Current stable low-cost Gemini model"},
			{ID: "gemini-3.1-pro-preview", Label: "Gemini 3.1 Pro Preview", Description: "Preview model optimized for agentic and coding workflows"},
			{ID: "gemini-3.1-pro-preview-customtools", Label: "Gemini 3.1 Pro Preview Custom Tools", Description: "Gemini 3.1 Pro endpoint tuned for custom tool-heavy workflows"},
			{ID: "gemini-2.5-pro", Label: "Gemini 2.5 Pro", Description: "Advanced reasoning and coding"},
			{ID: "gemini-2.5-flash", Label: "Gemini 2.5 Flash", Description: "Low-latency reasoning"},
			{ID: "gemini-2.5-flash-lite", Label: "Gemini 2.5 Flash-Lite", Description: "Fastest budget Gemini option"},
		},
	},
	{
		ID:            AIProviderGrok,
		Label:         "xAI Grok",
		AuthType:      AIAuthTypeAPIKey,
		CredentialKey: KeyGrokKey,
		Models:        grokModels,
	},
	{
		ID:       AIProviderCodex,
		Label:    "OpenAI (OAuth)",
		AuthType: AIAuthTypeUserOAuth,
		Models: []AIModelOption{
			{ID: "default", Label: "OpenAI recommended", Description: "Uses the current model recommended by Codex"},
			{ID: "gpt-5.6-sol", Label: "GPT-5.6 Sol", Description: "Highest-quality GPT-5.6 model for complex work"},
			{ID: "gpt-5.6-terra", Label: "GPT-5.6 Terra", Description: "Pragmatic GPT-5.6 model for everyday work"},
			{ID: "gpt-5.6-luna", Label: "GPT-5.6 Luna", Description: "Fast GPT-5.6 model for clear, repeatable work"},
		},
	},
	{
		ID:       AIProviderGrokOAuth,
		Label:    "xAI Grok (OAuth)",
		AuthType: AIAuthTypeUserOAuth,
		Models:   grokModels,
	},
}

// grokModels is shared by both xAI providers: the API key and the
// subscription OAuth paths serve the same OpenAI-compatible model catalog.
var grokModels = []AIModelOption{
	{ID: "grok-4.6", Label: "Grok 4.6", Description: "Latest flagship xAI model"},
	{ID: "grok-4.5", Label: "Grok 4.5", Description: "Previous-generation flagship model"},
	{ID: "grok-4.3", Label: "Grok 4.3", Description: "Affordable model with a 1M-token context"},
}

func isSecretKey(key string) bool {
	for _, k := range AllKeys {
		if k == key {
			return true
		}
	}
	return false
}

// DefaultAIModel returns the default model for a provider.
func DefaultAIModel(provider string) string {
	for _, p := range AIProviders {
		if p.ID == provider && len(p.Models) > 0 {
			return p.Models[0].ID
		}
	}
	return AIProviders[0].Models[0].ID
}

// AIKeyCredentialKey returns the secret setting key for a provider API key.
// Providers authenticated some other way, and unknown providers, return empty.
func AIKeyCredentialKey(provider string) string {
	for _, p := range AIProviders {
		if p.ID == provider {
			return p.CredentialKey
		}
	}
	return ""
}

// IsValidAIProvider reports whether provider is supported.
func IsValidAIProvider(provider string) bool {
	for _, p := range AIProviders {
		if p.ID == provider {
			return true
		}
	}
	return false
}

// IsOAuthAIProvider reports whether provider authenticates with a linked
// user account instead of a stored API key.
func IsOAuthAIProvider(provider string) bool {
	for _, p := range AIProviders {
		if p.ID == provider {
			return p.AuthType == AIAuthTypeUserOAuth
		}
	}
	return false
}

func inferAIProvider(model string) string {
	switch {
	case model == "":
		return ""
	case hasAnyPrefix(model, "claude-"):
		return AIProviderAnthropic
	case hasAnyPrefix(model, "gpt-", "o1", "o3", "o4"):
		return AIProviderOpenAI
	case hasAnyPrefix(model, "gemini-"):
		return AIProviderGemini
	case hasAnyPrefix(model, "grok-"):
		return AIProviderGrok
	default:
		return ""
	}
}

func hasAnyPrefix(value string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if len(value) >= len(prefix) && value[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// Registry lazily creates and caches TMDB/Trakt clients from DB-stored credentials.
type Registry struct {
	db     *sql.DB
	cipher *secrets.Cipher

	// defaultTMDBToken is the built-in public TMDB token
	// (tmdb.DefaultAccessToken in production). Empty means no fallback: TMDB
	// stays unconfigured until an admin stores a token.
	defaultTMDBToken string

	// defaultTraktClientID is the built-in public Trakt client ID
	// (trakt.DefaultClientID in production), with the same empty-means-no-
	// fallback contract as the TMDB token above.
	defaultTraktClientID string

	// tmdbBaseURL overrides the TMDB API root for lazily built clients.
	// Test-only; empty in production.
	tmdbBaseURL string

	mu          sync.RWMutex
	cachedTMDB  *tmdb.Client
	cachedTrakt *trakt.Client
	loaded      bool // true once we've attempted to load from DB
}

// Option customizes a Registry at construction.
type Option func(*Registry)

// WithDefaultTMDBToken supplies the built-in public TMDB token used whenever
// no admin-supplied token is stored. Only the server binary wires it, so
// registries built in tests stay TMDB-less (and network-less) unless a test
// opts in.
func WithDefaultTMDBToken(token string) Option {
	return func(r *Registry) { r.defaultTMDBToken = token }
}

// WithDefaultTraktClientID supplies the built-in public Trakt client ID used
// whenever no admin-supplied ID is stored. Only the server binary wires it,
// so registries built in tests stay Trakt-less unless a test opts in.
func WithDefaultTraktClientID(clientID string) Option {
	return func(r *Registry) { r.defaultTraktClientID = clientID }
}

// WithTMDBBaseURL points lazily built TMDB clients at an alternate API root.
// Test-only: production always talks to api.themoviedb.org.
func WithTMDBBaseURL(baseURL string) Option {
	return func(r *Registry) { r.tmdbBaseURL = baseURL }
}

// NewRegistry creates a new credentials registry.
func NewRegistry(db *sql.DB, cipher *secrets.Cipher, opts ...Option) *Registry {
	r := &Registry{db: db, cipher: cipher}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// TMDB returns the cached TMDB client, creating it lazily from the DB credential.
// Returns nil if the credential is not set.
func (r *Registry) TMDB() *tmdb.Client {
	r.mu.RLock()
	if r.loaded {
		c := r.cachedTMDB
		r.mu.RUnlock()
		return c
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loaded {
		return r.cachedTMDB
	}
	r.load()
	return r.cachedTMDB
}

// TMDBAvailable reports whether TMDB calls can be made at all — via an
// admin-supplied token or the built-in public one.
func (r *Registry) TMDBAvailable() bool {
	return r.TMDB() != nil
}

// TMDBUsingBuiltIn reports whether TMDB is running on the built-in public
// token rather than an admin-supplied credential.
func (r *Registry) TMDBUsingBuiltIn() bool {
	return r.defaultTMDBToken != "" && !r.IsConfigured(KeyTMDBAccessToken)
}

// Trakt returns the cached Trakt client, creating it lazily from the DB credential.
// Returns nil if the credential is not set.
func (r *Registry) Trakt() *trakt.Client {
	r.mu.RLock()
	if r.loaded {
		c := r.cachedTrakt
		r.mu.RUnlock()
		return c
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loaded {
		return r.cachedTrakt
	}
	r.load()
	return r.cachedTrakt
}

// TraktAvailable reports whether Trakt calls can be made at all — via an
// admin-supplied client ID or the built-in application.
func (r *Registry) TraktAvailable() bool {
	return r.Trakt() != nil
}

// TraktUsingBuiltIn reports whether Trakt is running on the built-in
// application rather than an admin-supplied client ID.
func (r *Registry) TraktUsingBuiltIn() bool {
	return r.defaultTraktClientID != "" && !r.IsConfigured(KeyTraktClientID)
}

// GetCredential reads a credential value from the DB, decrypting stored
// ciphertext (legacy plaintext passes through). Returns empty string if not
// set or undecryptable.
func (r *Registry) GetCredential(key string) string {
	var value string
	err := r.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err != nil {
		return ""
	}
	plain, err := r.cipher.Decrypt(value)
	if err != nil {
		log.Printf("credentials: failed to decrypt %s (wrong encryption key?): %v", key, err)
		return ""
	}
	return plain
}

// GetSetting reads a non-secret setting value from the DB.
func (r *Registry) GetSetting(key string) string {
	var value string
	if err := r.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value); err != nil {
		return ""
	}
	return value
}

// SetSetting writes a non-secret setting value to the DB.
func (r *Registry) SetSetting(key, value string) error {
	_, err := r.db.Exec(
		"INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)",
		key, value,
	)
	return err
}

// AIHealthCheckEnabled defaults on for existing installs. The switch affects
// only periodic probes; explicit shared-provider saves are always validated.
func (r *Registry) AIHealthCheckEnabled() bool {
	raw := strings.TrimSpace(r.GetSetting(KeyAIHealthCheckEnabled))
	if raw == "" {
		return true
	}
	enabled, err := strconv.ParseBool(raw)
	return err == nil && enabled
}

// AIHealthLastCheck returns the last completed scheduled or save-time probe.
// A missing or malformed value is treated as never checked.
func (r *Registry) AIHealthLastCheck() time.Time {
	checked, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(r.GetSetting(KeyAIHealthLastCheckAt)))
	if err != nil {
		return time.Time{}
	}
	return checked.UTC()
}

// RecordAIHealthCheck prevents restarts from multiplying background usage.
func (r *Registry) RecordAIHealthCheck(checked time.Time) error {
	return r.SetSetting(KeyAIHealthLastCheckAt, checked.UTC().Format(time.RFC3339Nano))
}

// AIHealthCheckDue reports whether the optional daily shared-model turn is due.
func (r *Registry) AIHealthCheckDue(now time.Time) bool {
	if !r.AIHealthCheckEnabled() {
		return false
	}
	last := r.AIHealthLastCheck()
	return last.IsZero() || !now.UTC().Before(last.Add(AIHealthCheckInterval))
}

// AISelectionConfigured distinguishes an intentionally configured shared
// provider from an untouched install's derived default.
func (r *Registry) AISelectionConfigured() bool {
	if strings.TrimSpace(r.GetSetting(KeyAIProvider)) != "" || strings.TrimSpace(r.GetSetting(KeyAIModel)) != "" {
		return true
	}
	if strings.TrimSpace(os.Getenv("CANTINARR_AI_PROVIDER")) != "" || strings.TrimSpace(os.Getenv("CANTINARR_AI_MODEL")) != "" {
		return true
	}
	config := r.GetAIConfig()
	key := AIKeyCredentialKey(config.Provider)
	return key != "" && r.IsConfigured(key)
}

// SetCredential writes a credential to the DB (upsert). Secret keys are
// encrypted at rest; non-secret settings are stored as-is.
func (r *Registry) SetCredential(key, value string) error {
	if isSecretKey(key) && value != "" {
		enc, err := r.cipher.Encrypt(value)
		if err != nil {
			return err
		}
		value = enc
	}
	_, err := r.db.Exec(
		"INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)",
		key, value,
	)
	return err
}

// GetAIConfig resolves the active AI provider/model. Stored settings win over
// environment defaults, and custom model IDs are allowed.
func (r *Registry) GetAIConfig() AIConfig {
	provider := r.GetSetting(KeyAIProvider)
	model := r.GetSetting(KeyAIModel)

	if model == "" {
		model = os.Getenv("CANTINARR_AI_MODEL")
	}
	if provider == "" {
		provider = os.Getenv("CANTINARR_AI_PROVIDER")
	}
	if provider == "" {
		provider = inferAIProvider(model)
	}
	if provider == "" {
		provider = DefaultAIProvider
		if model == "" {
			model = DefaultSharedAIModel
		}
	}
	// Preserve an explicitly invalid stored/environment value instead of
	// presenting a healthy-looking default that the strict runtime resolver
	// will refuse. The settings surface can then report and repair the real
	// configuration rather than masking it.
	if !IsValidAIProvider(provider) {
		return AIConfig{Provider: provider, Model: model}
	}
	if model == "" {
		model = DefaultAIModel(provider)
	}
	return AIConfig{Provider: provider, Model: model}
}

// SetAIConfig persists the active AI provider/model. Unknown providers are
// rejected; model is intentionally free-form so admins can use new provider IDs.
func (r *Registry) SetAIConfig(provider, model string) error {
	if !IsValidAIProvider(provider) {
		return fmt.Errorf("invalid AI provider %q", provider)
	}
	if model == "" {
		model = DefaultAIModel(provider)
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", KeyAIProvider, provider); err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", KeyAIModel, model); err != nil {
		return err
	}
	return tx.Commit()
}

// IsAIConfigured reports whether the selected shared provider has a server API
// key. OAuth-backed Codex readiness is checked by the AI adapter instead.
func (r *Registry) IsAIConfigured() bool {
	cfg := r.GetAIConfig()
	key := AIKeyCredentialKey(cfg.Provider)
	return key != "" && r.IsConfigured(key)
}

// DeleteCredential removes a credential from the DB.
func (r *Registry) DeleteCredential(key string) error {
	_, err := r.db.Exec("DELETE FROM settings WHERE key = ?", key)
	return err
}

// IsConfigured checks whether a credential key has a value in the DB.
func (r *Registry) IsConfigured(key string) bool {
	var count int
	r.db.QueryRow("SELECT COUNT(*) FROM settings WHERE key = ?", key).Scan(&count)
	return count > 0
}

// Invalidate clears all cached clients, forcing recreation on next access.
func (r *Registry) Invalidate() {
	r.mu.Lock()
	r.cachedTMDB = nil
	r.cachedTrakt = nil
	r.loaded = false
	r.mu.Unlock()
}

// load reads credentials from DB and creates clients. Must be called under write lock.
func (r *Registry) load() {
	r.loaded = true

	newTMDB := tmdb.NewClient
	if r.tmdbBaseURL != "" {
		newTMDB = func(token string) *tmdb.Client { return tmdb.NewClientWithBaseURL(token, r.tmdbBaseURL) }
	}
	if token := r.getSettingLocked(KeyTMDBAccessToken); token != "" {
		r.cachedTMDB = newTMDB(token)
	} else if r.defaultTMDBToken != "" {
		// No admin token stored (or it failed to decrypt): run on the built-in
		// public token so discovery works without any TMDB signup.
		r.cachedTMDB = newTMDB(r.defaultTMDBToken)
	}
	if clientID := r.getSettingLocked(KeyTraktClientID); clientID != "" {
		r.cachedTrakt = trakt.NewClient(clientID)
	} else if r.defaultTraktClientID != "" {
		// No admin client ID stored (or it failed to decrypt): run on the
		// built-in application so Trakt discovery works without any signup.
		r.cachedTrakt = trakt.NewClient(r.defaultTraktClientID)
	}
}

func (r *Registry) getSettingLocked(key string) string {
	var value string
	r.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	plain, err := r.cipher.Decrypt(value)
	if err != nil {
		log.Printf("credentials: failed to decrypt %s (wrong encryption key?): %v", key, err)
		return ""
	}
	return plain
}
