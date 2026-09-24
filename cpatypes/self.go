package cpatypes

import (
	"net/url"
	"strconv"
	"strings"
)

// AgentHoldMode — по какому сроку дозревает нога рекрутёра.
type AgentHoldMode string

const (
	// AgentHoldFollowConversion — по холду самой конверсии (умолчание сети).
	AgentHoldFollowConversion AgentHoldMode = "follow_conversion"
	// AgentHoldOwnDays — свой срок сети.
	AgentHoldOwnDays AgentHoldMode = "own_days"
)

// AgentHold — когда нога рекрутёра становится доступной к выводу.
//
// ⚠ Действующий срок — МАКСИМУМ из Days и холда конверсии: короче холда нельзя,
// потому что отмена откатывает только невыплаченную ногу.
type AgentHold struct {
	Mode AgentHoldMode `json:"mode" cpa:"required"`
	// Days — число дней при AgentHoldOwnDays, иначе null.
	Days *int `json:"days"`
}

// DeploymentEnvironment — среда, которую называет СЕРВЕР.
type DeploymentEnvironment string

const (
	DeploymentProduction DeploymentEnvironment = "production"
	DeploymentStaging    DeploymentEnvironment = "staging"
	DeploymentSandbox    DeploymentEnvironment = "sandbox"
)

// SelfInfo — ответ GET /cpa/self: проба живости, версия контракта и настройки
// сети, которые нельзя знать заранее.
type SelfInfo struct {
	// ContractVersion сверяется при запуске: расхождение — повод перечитать
	// контракт, а не продолжать на старых предположениях.
	ContractVersion string `json:"contract_version" cpa:"required"`
	// Environment — куда вы на самом деле пришли. Отвечает сервер, а не ваша
	// конфигурация: адрес клиента можно перепутать, этот ответ — нет. Код,
	// создающий тестовые данные, обязан проверять его до первой записи.
	Environment DeploymentEnvironment `json:"environment" cpa:"required"`
	// NetworkID — непрозрачный идентификатор сети на стороне Recca. Слова
	// external в имени нет намеренно: сеть заводится у нас, а не у интегратора.
	NetworkID string   `json:"network_id" cpa:"required"`
	Scopes    []string `json:"scopes" cpa:"required"`
	// AgentBase — от чего считается нога рекрутёра.
	//
	// ⚠ Потолок ноги при ЛЮБОЙ базе остаётся маржой: нога платится из денег
	// оператора, и процент от выплаты, превысивший маржу, означал бы доплату из
	// его кармана.
	AgentBase AgentLegBase `json:"agent_base" cpa:"required"`
	AgentHold *AgentHold   `json:"agent_hold"`
	// MinPayoutRUB — порог выплаты сети. Указатель, потому что «порога нет» и
	// «порог равен нулю» — разные утверждения, а второе к тому же неотличимо от
	// первого при целом типе.
	MinPayoutRUB *RUB `json:"min_payout_rub"`
	// RateLimitPerMin — ФАКТИЧЕСКИЙ бюджет этого ключа в минуту, то самое число,
	// по которому принимается решение о 429. Брать его отсюда, а не из таблицы
	// величин: там гарантированный минимум и потолок.
	RateLimitPerMin int `json:"rate_limit_per_min" cpa:"required"`
	// WebhookConfigured — настроен ли адрес событий. Указатель: «не настроен» и
	// «дверь об этом не сказала» — разные ответы.
	WebhookConfigured *bool `json:"webhook_configured"`
}

// WebhookEvent — события, которые уходят на адрес интегратора.
type WebhookEvent string

const (
	WebhookConversionApproved WebhookEvent = "cpa.conversion.approved"
	WebhookConversionDeclined WebhookEvent = "cpa.conversion.declined"
	WebhookConversionReversed WebhookEvent = "cpa.conversion.reversed"
	WebhookPayoutPaid         WebhookEvent = "cpa.payout.paid"
)

// WebhookConfig — ответ PUT /cpa/self/webhook.
type WebhookConfig struct {
	URL    string         `json:"url" cpa:"required"`
	Events []WebhookEvent `json:"events" cpa:"required"`
	// Secret показывается ОДИН раз — при ПЕРВОЙ установке адреса и при каждой
	// ротации. Сохраните сразу: повторно дверь его не выдаёт, и null здесь
	// означает «не показываем», а не «секрета нет». Смена адреса без ротации
	// секрет не показывает.
	Secret *string `json:"secret"`
}

// SetWebhookParams — вход PUT /cpa/self/webhook.
type SetWebhookParams struct {
	// URL — куда слать события cpa.*.
	URL string `json:"url"`
	// RotateSecret просит выдать новый секрет. ⚠ Прежний перестаёт действовать:
	// ротация — это отзыв, а не добавление второго ключа.
	RotateSecret bool `json:"rotate_secret,omitempty"`
}

// Validate проверяет форму адреса до отправки.
//
// ⚠ Проверяется ровно то, в чём клиент прав наверняка: адрес разбирается, он
// абсолютный и схема из пары http/https. Требовать только https значило бы
// отказывать в том, что дверь может принимать, — политику приёмника решает
// Recca, а не клиент.
func (p SetWebhookParams) Validate() error {
	if strings.TrimSpace(p.URL) == "" {
		return invalid("url", "обязательное поле")
	}
	parsed, err := url.Parse(p.URL)
	if err != nil {
		return invalid("url", "адрес не разбирается: "+err.Error())
	}
	switch {
	case parsed.Scheme != "http" && parsed.Scheme != "https":
		return invalid("url", "схема "+strconv.Quote(parsed.Scheme)+" вместо http или https")
	case parsed.Host == "":
		return invalid("url", "адрес без хоста — событию некуда приехать")
	}
	return nil
}
