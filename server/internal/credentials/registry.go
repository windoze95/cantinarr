package credentials

import (
	"database/sql"
	"log"
	"os"
	"sync"

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
	KeyTraktClientID   = "trakt_client_id"

	KeyAIProvider = "ai_provider"
	KeyAIModel    = "ai_model"
)

// AllKeys lists every credential key the system manages. Values for these
// keys are encrypted at rest; other settings (e.g. tool toggles) stay plain.
var AllKeys = []string{KeyTMDBAccessToken, KeyAnthropicKey, KeyOpenAIKey, KeyGeminiKey, KeyTraktClientID}

const (
	AIProviderAnthropic = "anthropic"
	AIProviderOpenAI    = "openai"
	AIProviderGemini    = "gemini"

	DefaultAIProvider = AIProviderAnthropic
)

// AIModelOption describes one selectable chat model for the admin UI.
type AIModelOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// AIProviderOption describes a supported AI provider and its default models.
type AIProviderOption struct {
	ID            string          `json:"id"`
	Label         string          `json:"label"`
	CredentialKey string          `json:"credential_key"`
	Models        []AIModelOption `json:"models"`
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
		CredentialKey: KeyAnthropicKey,
		Models: []AIModelOption{
			{ID: "claude-opus-4-8", Label: "Claude Opus 4.8", Description: "Most capable Claude Opus-tier model"},
			{ID: "claude-fable-5", Label: "Claude Fable 5", Description: "Highest-capability Claude model"},
			{ID: "claude-sonnet-4-6", Label: "Claude Sonnet 4.6", Description: "Balanced speed and intelligence"},
			{ID: "claude-haiku-4-5", Label: "Claude Haiku 4.5", Description: "Fastest, lowest-cost Claude option"},
		},
	},
	{
		ID:            AIProviderOpenAI,
		Label:         "OpenAI",
		CredentialKey: KeyOpenAIKey,
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
		CredentialKey: KeyGeminiKey,
		Models: []AIModelOption{
			{ID: "gemini-3.5-flash", Label: "Gemini 3.5 Flash", Description: "Current stable Gemini Flash model"},
			{ID: "gemini-3.1-pro-preview", Label: "Gemini 3.1 Pro Preview", Description: "Preview model optimized for agentic and coding workflows"},
			{ID: "gemini-3.1-pro-preview-customtools", Label: "Gemini 3.1 Pro Preview Custom Tools", Description: "Gemini 3.1 Pro endpoint tuned for custom tool-heavy workflows"},
			{ID: "gemini-2.5-pro", Label: "Gemini 2.5 Pro", Description: "Advanced reasoning and coding"},
			{ID: "gemini-2.5-flash", Label: "Gemini 2.5 Flash", Description: "Low-latency reasoning"},
			{ID: "gemini-2.5-flash-lite", Label: "Gemini 2.5 Flash-Lite", Description: "Fastest budget Gemini option"},
		},
	},
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
func AIKeyCredentialKey(provider string) string {
	for _, p := range AIProviders {
		if p.ID == provider {
			return p.CredentialKey
		}
	}
	return KeyAnthropicKey
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

	mu          sync.RWMutex
	cachedTMDB  *tmdb.Client
	cachedTrakt *trakt.Client
	loaded      bool // true once we've attempted to load from DB
}

// NewRegistry creates a new credentials registry.
func NewRegistry(db *sql.DB, cipher *secrets.Cipher) *Registry {
	return &Registry{db: db, cipher: cipher}
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
	if !IsValidAIProvider(provider) {
		provider = DefaultAIProvider
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
		provider = DefaultAIProvider
	}
	if model == "" {
		model = DefaultAIModel(provider)
	}
	if err := r.SetSetting(KeyAIProvider, provider); err != nil {
		return err
	}
	return r.SetSetting(KeyAIModel, model)
}

// IsAIConfigured reports whether the selected provider has an API key.
func (r *Registry) IsAIConfigured() bool {
	cfg := r.GetAIConfig()
	return r.IsConfigured(AIKeyCredentialKey(cfg.Provider))
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

	if token := r.getSettingLocked(KeyTMDBAccessToken); token != "" {
		r.cachedTMDB = tmdb.NewClient(token)
	}
	if clientID := r.getSettingLocked(KeyTraktClientID); clientID != "" {
		r.cachedTrakt = trakt.NewClient(clientID)
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
