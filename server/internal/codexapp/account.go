package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"
)

type deviceFlow struct {
	id              string
	actorID         int64
	account         AccountRef
	loginID         string
	verificationURI string
	userCode        string
	expiresAt       time.Time
	session         *appSession

	mu         sync.Mutex
	completion *loginCompletion
	finalizing bool
	canceled   bool
	done       chan struct{}
	finishOnce sync.Once
}

type loginCompletion struct {
	success bool
}

type loginStartResponse struct {
	Type            string `json:"type"`
	LoginID         string `json:"loginId"`
	UserCode        string `json:"userCode"`
	VerificationURL string `json:"verificationUrl"`
}

type accountResponse struct {
	Account *struct {
		Type     string  `json:"type"`
		Email    *string `json:"email"`
		PlanType string  `json:"planType"`
	} `json:"account"`
	RequiresOpenAIAuth bool `json:"requiresOpenaiAuth"`
}

// Status returns cached metadata or, when refresh is true, asks app-server to
// refresh the user's account and rate-limit state. Account-backed refreshes are
// serialized with chat and login for that user.
func (m *Manager) Status(ctx context.Context, userID int64, refresh bool) (status AccountStatus, err error) {
	return m.StatusForAccount(ctx, PersonalAccount(userID), refresh)
}

// StatusForAccount returns safe status for a personal or shared authorization.
func (m *Manager) StatusForAccount(ctx context.Context, account AccountRef, refresh bool) (status AccountStatus, err error) {
	if m == nil || m.db == nil || m.cipher == nil || !account.valid() {
		return AccountStatus{}, ErrInvalidInput
	}
	status, found, err := m.accountMetadata(account)
	if err != nil {
		return AccountStatus{}, err
	}
	if !found {
		return AccountStatus{Connected: false}, nil
	}
	if !refresh || !status.Stale {
		return status, nil
	}
	if err := validateManager(m); err != nil {
		return AccountStatus{}, err
	}
	opCtx, cancel := m.accountContext(ctx)
	defer cancel()
	ctx = opCtx

	if err := m.acquireAccount(ctx, account); err != nil {
		return AccountStatus{}, err
	}
	defer m.releaseAccount(account)
	ctx, operation, err := m.registerAccountOperation(ctx, account)
	if err != nil {
		return AccountStatus{}, err
	}
	defer m.unregisterAccountOperation(account, operation)
	record, found, err := m.loadAccount(account)
	if err != nil {
		return AccountStatus{}, err
	}
	if !found {
		return AccountStatus{Connected: false}, nil
	}

	session, err := m.startSession(record.authJSON)
	if err != nil {
		return AccountStatus{}, err
	}
	var persistFull *AccountStatus
	purgeAccount := false
	defer func() {
		if purgeAccount {
			session.stop()
			session.cleanup()
			if deleteErr := m.deleteAccount(account); err == nil && deleteErr != nil {
				err = deleteErr
			}
			return
		}
		persistErr := m.finishAccountSession(account, session, persistFull, operation)
		if err == nil && persistErr != nil {
			err = persistErr
		}
	}()
	if err = session.initialize(ctx); err != nil {
		return AccountStatus{}, wrapContextOrSafe(ctx, ErrProvider)
	}

	var response accountResponse
	if err = session.request(ctx, "account/read", map[string]bool{"refreshToken": true}, &response); err != nil {
		if errors.Is(err, ErrNotConnected) {
			purgeAccount = true
			return AccountStatus{Connected: false}, nil
		}
		return AccountStatus{}, wrapContextOrSafe(ctx, ErrProvider)
	}
	if response.Account == nil || response.Account.Type != "chatgpt" {
		// app-server made an authoritative auth decision. Remove the stale row
		// so the disconnected user can immediately start a new device login.
		purgeAccount = true
		return AccountStatus{Connected: false}, nil
	}
	cachedUpdatedAt := status.UpdatedAt
	status = AccountStatus{
		Connected: true, PlanType: response.Account.PlanType,
		RateLimits: cloneRaw(record.rateLimits), UpdatedAt: cachedUpdatedAt, Stale: true,
	}
	if response.Account.Email != nil {
		status.Email = *response.Account.Email
	}
	var rateLimits json.RawMessage
	if rateErr := session.request(ctx, "account/rateLimits/read", nil, &rateLimits); rateErr == nil && json.Valid(rateLimits) {
		status.RateLimits = cloneRaw(rateLimits)
		status.UpdatedAt = time.Now().UTC()
		status.Stale = false
		persistFull = &status
	} else if ctx.Err() != nil {
		return AccountStatus{}, ctx.Err()
	}
	return status, nil
}

// BeginDeviceLogin starts one app-server device flow for userID. The process,
// upstream login ID, and plaintext auth state remain server-side in that
// operation's dedicated app-server session.
func (m *Manager) BeginDeviceLogin(ctx context.Context, userID int64) (DeviceLogin, error) {
	return m.BeginDeviceLoginForAccount(ctx, PersonalAccount(userID), userID)
}

// BeginDeviceLoginForAccount starts a device flow for account and binds the
// capability to actorID. Shared account identity never becomes tool identity.
func (m *Manager) BeginDeviceLoginForAccount(ctx context.Context, account AccountRef, actorID int64) (DeviceLogin, error) {
	if err := validateManager(m); err != nil {
		return DeviceLogin{}, err
	}
	if !account.valid() || actorID <= 0 {
		return DeviceLogin{}, ErrInvalidInput
	}
	opCtx, cancel := m.accountContext(ctx)
	defer cancel()
	ctx = opCtx
	if ctx.Err() != nil {
		return DeviceLogin{}, ctx.Err()
	}
	if !m.reserveLoginStart(account) {
		return DeviceLogin{}, ErrLoginInProgress
	}
	reservationHeld := true
	defer func() {
		if reservationHeld {
			m.releaseLoginStart(account)
		}
	}()
	if err := m.tryAcquireAccount(account); err != nil {
		return DeviceLogin{}, err
	}
	gateHeld := true
	defer func() {
		if gateHeld {
			m.releaseAccount(account)
		}
	}()
	ctx, operation, err := m.registerAccountOperation(ctx, account)
	if err != nil {
		return DeviceLogin{}, err
	}
	defer m.unregisterAccountOperation(account, operation)
	_, found, err := m.loadAccount(account)
	if err != nil {
		return DeviceLogin{}, err
	}
	if found {
		return DeviceLogin{}, ErrAlreadyConnected
	}
	select {
	case m.loginSlots <- struct{}{}:
	default:
		return DeviceLogin{}, ErrProvider
	}
	loginSlotHeld := true
	defer func() {
		if loginSlotHeld {
			<-m.loginSlots
		}
	}()

	session, err := m.startSession(nil)
	if err != nil {
		return DeviceLogin{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			session.stop()
			session.cleanup()
		}
	}()
	if err := session.initialize(ctx); err != nil {
		return DeviceLogin{}, wrapContextOrSafe(ctx, ErrProvider)
	}
	var response loginStartResponse
	if err := session.request(ctx, "account/login/start", map[string]string{"type": "chatgptDeviceCode"}, &response); err != nil {
		return DeviceLogin{}, wrapContextOrSafe(ctx, ErrProvider)
	}
	if response.Type != "chatgptDeviceCode" || response.LoginID == "" || !safeDeviceCode(response.UserCode) || !trustedVerificationURL(response.VerificationURL) {
		return DeviceLogin{}, ErrProvider
	}
	flowID, err := randomFlowID()
	if err != nil {
		return DeviceLogin{}, ErrProvider
	}
	flow := &deviceFlow{
		id:              flowID,
		actorID:         actorID,
		account:         account,
		loginID:         response.LoginID,
		verificationURI: response.VerificationURL,
		userCode:        response.UserCode,
		expiresAt:       time.Now().Add(deviceLoginLifetime),
		session:         session,
		done:            make(chan struct{}),
	}
	if m.beforeLoginPublish != nil {
		m.beforeLoginPublish()
	}
	if !m.publishLoginFlow(flow, operation) {
		return DeviceLogin{}, ErrNotConnected
	}
	reservationHeld = false
	gateHeld = false
	cleanup = false
	loginSlotHeld = false
	go m.watchDeviceFlow(flow)
	go m.expireDeviceFlow(flow)
	return DeviceLogin{
		FlowID:          flowID,
		VerificationURI: response.VerificationURL,
		UserCode:        response.UserCode,
		ExpiresAt:       flow.expiresAt,
		IntervalSeconds: int(devicePollInterval.Seconds()),
	}, nil
}

// CheckDeviceLogin returns the state of a caller-owned flow and persists a
// completed account before releasing that user's serialization gate.
func (m *Manager) CheckDeviceLogin(ctx context.Context, userID int64, flowID string) (DeviceLoginCheck, error) {
	return m.CheckDeviceLoginForAccount(ctx, PersonalAccount(userID), userID, flowID)
}

func (m *Manager) CheckDeviceLoginForAccount(ctx context.Context, account AccountRef, actorID int64, flowID string) (DeviceLoginCheck, error) {
	if err := validateManager(m); err != nil {
		return DeviceLoginCheck{}, err
	}
	opCtx, cancel := m.accountContext(ctx)
	defer cancel()
	ctx = opCtx
	flow := m.ownedFlow(actorID, flowID)
	if flow == nil || flow.account != account {
		return DeviceLoginCheck{}, ErrFlowNotFound
	}
	flow.mu.Lock()
	if flow.canceled {
		flow.mu.Unlock()
		m.finishFlow(flow)
		return DeviceLoginCheck{Status: LoginFailed, Error: "ChatGPT sign-in was canceled"}, nil
	}
	if time.Now().After(flow.expiresAt) {
		flow.mu.Unlock()
		m.finishFlow(flow)
		return DeviceLoginCheck{Status: LoginExpired}, nil
	}
	if flow.finalizing {
		flow.mu.Unlock()
		return DeviceLoginCheck{Status: LoginPending}, nil
	}
	completion := flow.completion
	if completion == nil {
		flow.mu.Unlock()
		return DeviceLoginCheck{Status: LoginPending}, nil
	}
	flow.finalizing = true
	flow.mu.Unlock()
	if !completion.success {
		m.finishFlow(flow)
		return DeviceLoginCheck{Status: LoginFailed, Error: "ChatGPT sign-in failed"}, nil
	}

	var response accountResponse
	if err := flow.session.request(ctx, "account/read", map[string]bool{"refreshToken": false}, &response); err != nil || response.Account == nil || response.Account.Type != "chatgpt" {
		m.finishFlow(flow)
		if ctx.Err() != nil {
			return DeviceLoginCheck{}, ctx.Err()
		}
		return DeviceLoginCheck{Status: LoginFailed, Error: "ChatGPT sign-in could not be completed"}, nil
	}
	status := AccountStatus{Connected: true, PlanType: response.Account.PlanType, Stale: true}
	if response.Account.Email != nil {
		status.Email = *response.Account.Email
	}
	var rateLimits json.RawMessage
	if err := flow.session.request(ctx, "account/rateLimits/read", nil, &rateLimits); err == nil && json.Valid(rateLimits) {
		status.RateLimits = cloneRaw(rateLimits)
		status.UpdatedAt = time.Now().UTC()
		status.Stale = false
	}
	flow.session.stop()
	authJSON, err := flow.session.readAuthJSON()
	flow.mu.Lock()
	canceled := flow.canceled
	if err == nil && !canceled {
		err = m.saveAccount(flow.account, authJSON, status)
	}
	flow.mu.Unlock()
	m.finishFlow(flow)
	if canceled {
		return DeviceLoginCheck{Status: LoginFailed, Error: "ChatGPT sign-in was canceled"}, nil
	}
	if err != nil {
		return DeviceLoginCheck{Status: LoginFailed, Error: "ChatGPT sign-in could not be saved"}, nil
	}
	return DeviceLoginCheck{Status: LoginConnected, Account: status}, nil
}

// CancelDeviceLogin cancels and removes only a flow owned by actorID.
func (m *Manager) CancelDeviceLogin(actorID int64, flowID string) error {
	return m.CancelDeviceLoginForAccount(PersonalAccount(actorID), actorID, flowID)
}

func (m *Manager) CancelDeviceLoginForAccount(account AccountRef, actorID int64, flowID string) error {
	if err := validateManager(m); err != nil {
		return err
	}
	flow := m.ownedFlow(actorID, flowID)
	if flow == nil || flow.account != account {
		return ErrFlowNotFound
	}
	if m.afterCancelLookup != nil {
		m.afterCancelLookup()
	}
	flow.mu.Lock()
	m.flowsMu.Lock()
	if m.flows[flow.id] != flow || m.accountFlows[flow.account] != flow.id {
		m.flowsMu.Unlock()
		flow.mu.Unlock()
		return ErrFlowNotFound
	}
	flow.canceled = true
	m.flowsMu.Unlock()
	// The flow still owns the per-user gate here, so no newer login can race
	// this authoritative local delete. Do it before the best-effort upstream
	// cancel, which may consume its entire timeout.
	deleteErr := m.deleteAccount(flow.account)
	flow.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var ignored any
	_ = flow.session.request(ctx, "account/login/cancel", map[string]string{"loginId": flow.loginID}, &ignored)
	m.finishFlow(flow)
	return deleteErr
}

// Unlink cancels any active login, asks app-server to log out when possible,
// and always removes the local encrypted account row.
func (m *Manager) Unlink(userID int64) error {
	return m.UnlinkAccount(PersonalAccount(userID))
}

// UnlinkAccount revokes one personal or shared authorization and cancels any
// operation using that account without touching the other scope.
func (m *Manager) UnlinkAccount(account AccountRef) error {
	if m == nil || m.db == nil || !account.valid() {
		return ErrInvalidInput
	}
	m.beginAccountRevocation(account)
	defer m.endAccountRevocation(account)
	if flow := m.flowForAccount(account); flow != nil {
		flow.mu.Lock()
		flow.canceled = true
		flow.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		var ignored any
		_ = flow.session.request(ctx, "account/login/cancel", map[string]string{"loginId": flow.loginID}, &ignored)
		cancel()
		m.finishFlow(flow)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if m.Available() {
		if err := m.acquireAccount(ctx, account); err == nil {
			record, found, _ := m.loadAccount(account)
			if found {
				if session, err := m.startSession(record.authJSON); err == nil {
					if session.initialize(ctx) == nil {
						var ignored any
						_ = session.request(ctx, "account/logout", nil, &ignored)
					}
					session.stop()
					session.cleanup()
				}
			}
			m.releaseAccount(account)
		}
	}
	return m.deleteAccount(account)
}

func (m *Manager) finishAccountSession(account AccountRef, session *appSession, status *AccountStatus, operation *userOperation) error {
	session.stop()
	authJSON, err := session.readAuthJSON()
	defer session.cleanup()
	if err != nil {
		return ErrStorage
	}
	// Keep validation and the database write atomic with respect to Unlink's
	// generation bump. If persistence wins, Unlink observes it and deletes the
	// row afterward; if revocation wins, this session cannot write anything.
	m.operationsMu.Lock()
	defer m.operationsMu.Unlock()
	if operation != nil && (m.revocations[account] != 0 || m.accountGenerations[account] != operation.generation) {
		return context.Canceled
	}
	if status != nil && status.Connected {
		return m.saveAccount(account, authJSON, *status)
	}
	return m.saveRefreshedAuth(account, authJSON)
}

func (m *Manager) ownedFlow(actorID int64, flowID string) *deviceFlow {
	if flowID == "" {
		return nil
	}
	m.flowsMu.Lock()
	defer m.flowsMu.Unlock()
	flow := m.flows[flowID]
	if flow == nil || flow.actorID != actorID {
		return nil
	}
	return flow
}

func (m *Manager) flowForAccount(account AccountRef) *deviceFlow {
	m.flowsMu.Lock()
	defer m.flowsMu.Unlock()
	return m.flows[m.accountFlows[account]]
}

func (m *Manager) flowForUser(userID int64) *deviceFlow {
	return m.flowForAccount(PersonalAccount(userID))
}

func (m *Manager) finishFlow(flow *deviceFlow) {
	flow.finishOnce.Do(func() {
		flow.mu.Lock()
		flow.canceled = true
		flow.mu.Unlock()
		close(flow.done)
		flow.session.stop()
		flow.session.cleanup()
		m.flowsMu.Lock()
		delete(m.flows, flow.id)
		if m.accountFlows[flow.account] == flow.id {
			delete(m.accountFlows, flow.account)
		}
		m.flowsMu.Unlock()
		m.releaseAccount(flow.account)
		select {
		case <-m.loginSlots:
		default:
		}
	})
}

func (m *Manager) watchDeviceFlow(flow *deviceFlow) {
	for {
		select {
		case <-flow.done:
			return
		case <-flow.session.processDone:
			flow.mu.Lock()
			if flow.completion == nil {
				flow.completion = &loginCompletion{success: false}
			}
			flow.mu.Unlock()
			return
		case notification := <-flow.session.notifications:
			if notification.method != "account/login/completed" {
				continue
			}
			var completed struct {
				LoginID *string `json:"loginId"`
				Success bool    `json:"success"`
			}
			if json.Unmarshal(notification.params, &completed) != nil || completed.LoginID == nil || *completed.LoginID != flow.loginID {
				continue
			}
			flow.mu.Lock()
			flow.completion = &loginCompletion{success: completed.Success}
			flow.mu.Unlock()
		}
	}
}

func (m *Manager) expireDeviceFlow(flow *deviceFlow) {
	timer := time.NewTimer(time.Until(flow.expiresAt))
	defer timer.Stop()
	select {
	case <-flow.done:
		return
	case <-timer.C:
		m.finishFlow(flow)
	}
}

func trustedVerificationURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Host == "auth.openai.com" && parsed.User == nil
}

func safeDeviceCode(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	return !strings.ContainsAny(value, "\r\n\x00")
}
