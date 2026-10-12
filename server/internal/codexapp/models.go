package codexapp

import (
	"context"
	"errors"
	"slices"
	"strings"
)

type catalogModel struct {
	ID              string   `json:"id"`
	Model           string   `json:"model"`
	Upgrade         string   `json:"upgrade"`
	Hidden          bool     `json:"hidden"`
	IsDefault       bool     `json:"isDefault"`
	InputModalities []string `json:"inputModalities"`
}

// RecommendedModel reads provider metadata under the same account lock and
// refresh-token persistence rules as a turn. This is a recommendation, not an
// entitlement check: the replacement still has to complete the real request.
func (m *Manager) RecommendedModel(ctx context.Context, account AccountRef, selected string) (model, source string, err error) {
	if err := validateManager(m); err != nil {
		return "", "", err
	}
	if !account.valid() {
		return "", "", ErrInvalidInput
	}
	ctx, cancel := m.accountContext(ctx)
	defer cancel()
	if err := m.acquireAccount(ctx, account); err != nil {
		return "", "", err
	}
	defer m.releaseAccount(account)
	ctx, operation, err := m.registerAccountOperation(ctx, account)
	if err != nil {
		return "", "", err
	}
	defer m.unregisterAccountOperation(account, operation)
	record, found, err := m.loadAccount(account)
	if err != nil {
		return "", "", err
	}
	if !found {
		return "", "", ErrNotConnected
	}
	session, err := m.startSession(record.authJSON)
	if err != nil {
		return "", "", err
	}
	defer func() {
		persistErr := m.finishAccountSession(account, session, nil, operation)
		if err == nil {
			err = persistErr
		}
	}()
	if err := session.initialize(ctx); err != nil {
		return "", "", contextOrClassified(ctx, err)
	}
	var models []catalogModel
	cursor := ""
	seen := map[string]bool{}
	for page := 0; page < 20; page++ {
		params := map[string]any{"limit": 100, "includeHidden": true}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var result struct {
			Data       []catalogModel `json:"data"`
			NextCursor *string        `json:"nextCursor"`
		}
		if err := session.request(ctx, "model/list", params, &result); err != nil {
			return "", "", contextOrClassified(ctx, err)
		}
		models = append(models, result.Data...)
		if result.NextCursor == nil || *result.NextCursor == "" {
			return selectRecommendedModel(models, selected)
		}
		cursor = *result.NextCursor
		if seen[cursor] {
			break
		}
		seen[cursor] = true
	}
	return "", "", ErrProvider // Incomplete catalogs never imply absence.
}

func selectRecommendedModel(models []catalogModel, selected string) (string, string, error) {
	compatible := func(m catalogModel) bool {
		return m.Model != "" && m.Model != selected && !m.Hidden &&
			(len(m.InputModalities) == 0 || slices.Contains(m.InputModalities, "text")) &&
			len(m.Model) <= 256 && !strings.ContainsAny(m.Model, "\r\n\x00")
	}
	for _, old := range models {
		if old.Model != selected && old.ID != selected {
			continue
		}
		for _, candidate := range models {
			if old.Upgrade != "" && (candidate.Model == old.Upgrade || candidate.ID == old.Upgrade) && compatible(candidate) {
				return candidate.Model, "OpenAI recommended upgrade", nil
			}
		}
	}
	var defaults []catalogModel
	for _, candidate := range models {
		if candidate.IsDefault && compatible(candidate) {
			defaults = append(defaults, candidate)
		}
	}
	if len(defaults) == 1 && selected != "default" {
		return defaults[0].Model, "OpenAI recommended default", nil
	}
	return "", "", errors.New("no compatible Codex recommendation is available")
}
