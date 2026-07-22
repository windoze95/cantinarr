// Package mediafiles serves completed arr media through short-lived,
// file-scoped capability URLs. The arr supplies only file metadata; bytes are
// read from explicit local filesystem roots configured by the operator.
package mediafiles

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/instance"
)

const (
	defaultTicketTTL          = 10 * time.Minute
	defaultMaxTickets         = 1024
	defaultMaxTicketsPerUser  = 32
	maxTicketRequestBodyBytes = 4096
	ticketRandomBytes         = 32
	maxDownloadFilenameBytes  = 240
)

var errMediaFileUnavailable = errors.New("media file unavailable")

type instanceAccessStore interface {
	Get(id string) (*instance.Instance, error)
	UserCanAccessInstance(userID int64, instanceID, serviceType string) (bool, error)
}

type currentUserStore interface {
	GetUser(userID int64) (*auth.User, error)
}

type metadataResolver interface {
	Resolve(instanceID, serviceType string, fileID int) (fileMetadata, error)
}

type fileMetadata struct {
	Path string
}

type registryMetadataResolver struct {
	registry *instance.Registry
}

func (r registryMetadataResolver) Resolve(instanceID, serviceType string, fileID int) (fileMetadata, error) {
	switch serviceType {
	case "radarr":
		client, _, err := r.registry.GetFreshRadarrClient(instanceID)
		if err != nil {
			return fileMetadata{}, err
		}
		file, err := client.GetMovieFile(fileID)
		if err != nil {
			return fileMetadata{}, err
		}
		return fileMetadata{Path: file.Path}, nil
	case "sonarr":
		client, _, err := r.registry.GetFreshSonarrClient(instanceID)
		if err != nil {
			return fileMetadata{}, err
		}
		file, err := client.GetEpisodeFile(fileID)
		if err != nil {
			return fileMetadata{}, err
		}
		return fileMetadata{Path: file.Path}, nil
	case "chaptarr":
		client, _, err := r.registry.GetFreshChaptarrClient(instanceID)
		if err != nil {
			return fileMetadata{}, err
		}
		file, err := client.GetBookFile(fileID)
		if err != nil {
			return fileMetadata{}, err
		}
		return fileMetadata{Path: file.Path}, nil
	default:
		return fileMetadata{}, errMediaFileUnavailable
	}
}

type mediaRoot struct {
	path string
	root *os.Root
}

type downloadTicket struct {
	userID      int64
	instanceID  string
	serviceType string
	fileID      int
	expiresAt   time.Time
}

// Handler issues and serves bounded, short-lived download capabilities.
// Tickets retain only identity and authorization state: the arr metadata and
// local file are re-resolved for every GET or HEAD.
type Handler struct {
	store    instanceAccessStore
	users    currentUserStore
	resolver metadataResolver
	roots    []mediaRoot

	mu                sync.Mutex
	tickets           map[string]downloadTicket
	now               func() time.Time
	random            io.Reader
	ticketTTL         time.Duration
	maxTickets        int
	maxTicketsPerUser int
}

// NewHandler opens each configured root once for the process lifetime. Empty
// roots are valid and leave media downloads disabled.
func NewHandler(store *instance.Store, registry *instance.Registry, users *auth.Service, roots []string) (*Handler, error) {
	return newHandler(store, users, registryMetadataResolver{registry: registry}, roots)
}

func newHandler(store instanceAccessStore, users currentUserStore, resolver metadataResolver, roots []string) (*Handler, error) {
	h := &Handler{
		store:             store,
		users:             users,
		resolver:          resolver,
		tickets:           make(map[string]downloadTicket),
		now:               time.Now,
		random:            rand.Reader,
		ticketTTL:         defaultTicketTTL,
		maxTickets:        defaultMaxTickets,
		maxTicketsPerUser: defaultMaxTicketsPerUser,
	}

	seen := make(map[string]bool, len(roots))
	for _, configured := range roots {
		if !filepath.IsAbs(configured) {
			h.Close()
			return nil, fmt.Errorf("media root must be absolute")
		}
		cleaned := filepath.Clean(configured)
		if filepath.Dir(cleaned) == cleaned {
			h.Close()
			return nil, fmt.Errorf("media root is too broad")
		}
		resolved, err := filepath.EvalSymlinks(cleaned)
		if err != nil {
			h.Close()
			return nil, fmt.Errorf("resolve media root: %w", err)
		}
		if filepath.Dir(resolved) == resolved {
			h.Close()
			return nil, fmt.Errorf("media root resolves to filesystem root")
		}
		// Keep the cleaned lexical path because arr file records use the path
		// visible inside their container. On platforms such as macOS, resolving
		// /var to /private/var here would make a valid reported path miss its
		// configured boundary. EvalSymlinks above validates the target and rejects
		// aliases of the filesystem root; OpenRoot itself holds the safe directory
		// handle used for all subsequent opens.
		if seen[cleaned] {
			continue
		}
		root, err := os.OpenRoot(cleaned)
		if err != nil {
			h.Close()
			return nil, fmt.Errorf("open media root: %w", err)
		}
		seen[cleaned] = true
		h.roots = append(h.roots, mediaRoot{path: cleaned, root: root})
	}
	return h, nil
}

// Close releases the directory handles retained by the handler.
func (h *Handler) Close() error {
	var joined error
	for _, root := range h.roots {
		joined = errors.Join(joined, root.root.Close())
	}
	h.roots = nil
	return joined
}

type issueTicketRequest struct {
	InstanceID string `json:"instance_id"`
	FileID     int    `json:"file_id"`
}

type issueTicketResponse struct {
	URL       string    `json:"url"`
	Filename  string    `json:"filename"`
	SizeBytes int64     `json:"size_bytes"`
	ExpiresAt time.Time `json:"expires_at"`
}

// IssueTicket validates the caller's current effective instance access,
// resolves the live arr file record, verifies the local file boundary, and
// returns an opaque capability URL. No client-supplied filesystem path is
// accepted.
func (h *Handler) IssueTicket(w http.ResponseWriter, r *http.Request) {
	if len(h.roots) == 0 {
		writeJSONError(w, http.StatusServiceUnavailable, "media downloads are not configured")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxTicketRequestBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request issueTicketRequest
	if err := decoder.Decode(&request); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request")
		return
	}
	request.InstanceID = strings.TrimSpace(request.InstanceID)
	if request.InstanceID == "" || len(request.InstanceID) > 128 || request.FileID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "instance_id and a positive file_id are required")
		return
	}

	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	currentUser, err := h.users.GetUser(claims.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "temporarily unavailable, retry shortly")
		return
	}
	if currentUser == nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !auth.HasPermission(currentUser.Role, auth.PermissionMediaDownload) {
		writeJSONError(w, http.StatusForbidden, "permission denied")
		return
	}
	inst, err := h.store.Get(request.InstanceID)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "temporarily unavailable, retry shortly")
		return
	}
	if inst == nil || !supportedService(inst.ServiceType) {
		writeJSONError(w, http.StatusNotFound, "media file unavailable")
		return
	}

	isAdmin := auth.HasPermission(currentUser.Role, auth.PermissionInstancesManage)
	if !isAdmin {
		allowed, err := h.store.UserCanAccessInstance(claims.UserID, inst.ID, inst.ServiceType)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "temporarily unavailable, retry shortly")
			return
		}
		if !allowed {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
	}

	metadata, err := h.resolver.Resolve(inst.ID, inst.ServiceType, request.FileID)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "media file unavailable")
		return
	}
	opened, err := h.openMediaFile(metadata.Path, request.FileID)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "media file unavailable")
		return
	}
	_ = opened.file.Close()

	ticket := downloadTicket{
		userID:      claims.UserID,
		instanceID:  inst.ID,
		serviceType: inst.ServiceType,
		fileID:      request.FileID,
	}
	token, expiresAt, status, err := h.issue(ticket)
	if err != nil {
		writeJSONError(w, status, err.Error())
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(issueTicketResponse{
		URL:       "/api/media-files/download/" + token,
		Filename:  opened.filename,
		SizeBytes: opened.info.Size(),
		ExpiresAt: expiresAt,
	})
}

// Download serves a previously-issued capability. Tickets are deliberately
// reusable until expiration so browser HEAD probes and resumed Range requests
// cannot consume them prematurely.
func (h *Handler) Download(w http.ResponseWriter, r *http.Request) {
	w.Header().Del("Content-Type") // The /api router defaults to JSON.
	setDownloadSecurityHeaders(w.Header())
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeDownloadError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	token := chi.URLParam(r, "ticket")
	if !validTicketToken(token) {
		writeDownloadError(w, r, http.StatusNotFound, "download unavailable")
		return
	}
	ticket, ok := h.getTicket(token)
	if !ok {
		writeDownloadError(w, r, http.StatusNotFound, "download unavailable")
		return
	}

	currentUser, err := h.users.GetUser(ticket.userID)
	if errors.Is(err, sql.ErrNoRows) {
		writeDownloadError(w, r, http.StatusNotFound, "download unavailable")
		return
	}
	if err != nil {
		writeDownloadError(w, r, http.StatusServiceUnavailable, "temporarily unavailable, retry shortly")
		return
	}
	if currentUser == nil {
		writeDownloadError(w, r, http.StatusNotFound, "download unavailable")
		return
	}
	if !auth.HasPermission(currentUser.Role, auth.PermissionMediaDownload) {
		writeDownloadError(w, r, http.StatusNotFound, "download unavailable")
		return
	}
	inst, err := h.store.Get(ticket.instanceID)
	if err != nil {
		writeDownloadError(w, r, http.StatusServiceUnavailable, "temporarily unavailable, retry shortly")
		return
	}
	if inst == nil || inst.ServiceType != ticket.serviceType || !supportedService(inst.ServiceType) {
		writeDownloadError(w, r, http.StatusNotFound, "download unavailable")
		return
	}
	if !auth.HasPermission(currentUser.Role, auth.PermissionInstancesManage) {
		allowed, err := h.store.UserCanAccessInstance(ticket.userID, ticket.instanceID, ticket.serviceType)
		if err != nil {
			writeDownloadError(w, r, http.StatusServiceUnavailable, "temporarily unavailable, retry shortly")
			return
		}
		if !allowed {
			writeDownloadError(w, r, http.StatusNotFound, "download unavailable")
			return
		}
	}

	metadata, err := h.resolver.Resolve(ticket.instanceID, ticket.serviceType, ticket.fileID)
	if err != nil {
		writeDownloadError(w, r, http.StatusBadGateway, "download unavailable")
		return
	}
	opened, err := h.openMediaFile(metadata.Path, ticket.fileID)
	if err != nil {
		writeDownloadError(w, r, http.StatusNotFound, "download unavailable")
		return
	}
	defer opened.file.Close()

	contentDisposition := mime.FormatMediaType("attachment", map[string]string{"filename": opened.filename})
	if contentDisposition == "" {
		contentDisposition = "attachment"
	}
	w.Header().Set("Content-Disposition", contentDisposition)
	// Never let completed media become active same-origin browser content. In
	// particular, an arr library can contain HTML or SVG; attachment alone is
	// not a sufficient boundary across every user agent.
	w.Header().Set("Content-Type", "application/octet-stream")

	secureWriter := downloadResponseWriter{ResponseWriter: w}
	http.ServeContent(secureWriter, r, opened.filename, opened.info.ModTime(), opened.file)
}

func (h *Handler) issue(ticket downloadTicket) (string, time.Time, int, error) {
	now := h.now()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.purgeExpiredLocked(now)

	userTickets := 0
	for token, existing := range h.tickets {
		if existing.userID != ticket.userID {
			continue
		}
		userTickets++
		if existing.instanceID == ticket.instanceID && existing.serviceType == ticket.serviceType && existing.fileID == ticket.fileID {
			return token, existing.expiresAt, 0, nil
		}
	}
	if userTickets >= h.maxTicketsPerUser {
		return "", time.Time{}, http.StatusTooManyRequests, errors.New("too many active download tickets")
	}
	if len(h.tickets) >= h.maxTickets {
		return "", time.Time{}, http.StatusServiceUnavailable, errors.New("download service is busy, retry shortly")
	}

	for range 4 {
		randomBytes := make([]byte, ticketRandomBytes)
		if _, err := io.ReadFull(h.random, randomBytes); err != nil {
			return "", time.Time{}, http.StatusServiceUnavailable, errors.New("temporarily unavailable, retry shortly")
		}
		token := base64.RawURLEncoding.EncodeToString(randomBytes)
		if _, exists := h.tickets[token]; exists {
			continue
		}
		ticket.expiresAt = now.Add(h.ticketTTL).UTC()
		h.tickets[token] = ticket
		return token, ticket.expiresAt, 0, nil
	}
	return "", time.Time{}, http.StatusServiceUnavailable, errors.New("temporarily unavailable, retry shortly")
}

func (h *Handler) getTicket(token string) (downloadTicket, bool) {
	now := h.now()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.purgeExpiredLocked(now)
	ticket, ok := h.tickets[token]
	return ticket, ok
}

func (h *Handler) purgeExpiredLocked(now time.Time) {
	for token, ticket := range h.tickets {
		if !now.Before(ticket.expiresAt) {
			delete(h.tickets, token)
		}
	}
}

type openedMediaFile struct {
	file     *os.File
	info     os.FileInfo
	filename string
}

func (h *Handler) openMediaFile(reportedPath string, fileID int) (*openedMediaFile, error) {
	if !filepath.IsAbs(reportedPath) {
		return nil, errMediaFileUnavailable
	}
	cleaned := filepath.Clean(reportedPath)
	for _, allowedRoot := range h.roots {
		relative, err := filepath.Rel(allowedRoot.path, cleaned)
		if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		file, err := allowedRoot.root.Open(relative)
		if err != nil {
			continue
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			_ = file.Close()
			continue
		}
		return &openedMediaFile{
			file:     file,
			info:     info,
			filename: safeFilename(filepath.Base(cleaned), fileID),
		}, nil
	}
	return nil, errMediaFileUnavailable
}

func supportedService(serviceType string) bool {
	return serviceType == "radarr" || serviceType == "sonarr" || serviceType == "chaptarr"
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values")
	}
	return err
}

func validTicketToken(token string) bool {
	if len(token) != base64.RawURLEncoding.EncodedLen(ticketRandomBytes) {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(decoded) == ticketRandomBytes
}

func safeFilename(name string, fileID int) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if index := strings.LastIndexByte(name, '/'); index >= 0 {
		name = name[index+1:]
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return fmt.Sprintf("media-file-%d", fileID)
	}
	for len(name) > maxDownloadFilenameBytes {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	if name == "" {
		return fmt.Sprintf("media-file-%d", fileID)
	}
	return name
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func writeDownloadError(w http.ResponseWriter, r *http.Request, status int, message string) {
	setDownloadSecurityHeaders(w.Header())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
	}
}

func setDownloadSecurityHeaders(header http.Header) {
	header.Set("Cache-Control", "private, no-store")
	header.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	header.Set("Pragma", "no-cache")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Cross-Origin-Resource-Policy", "same-origin")
}

// ServeContent removes cache headers on some error paths. Reapply the
// download security boundary immediately before every status is committed.
type downloadResponseWriter struct {
	http.ResponseWriter
}

func (w downloadResponseWriter) WriteHeader(status int) {
	setDownloadSecurityHeaders(w.Header())
	if status >= http.StatusBadRequest {
		w.Header().Del("Content-Disposition")
	}
	w.ResponseWriter.WriteHeader(status)
}
