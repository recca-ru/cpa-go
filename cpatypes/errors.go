package cpatypes

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
)

// ErrorCode — код отказа двери. Ветвиться нужно по нему, а не по тексту
// сообщения: текст — диагностика для человека, он меняется без предупреждения и
// частью контракта не является (раздел 4).
type ErrorCode string

// Коды машинной двери /federation/v1/cpa/* — раздел 12 контракта.
const (
	// ── рама: ключ, право, продукт, частота ──────────────────────────────
	CodeMissingTenantScope  ErrorCode = "MISSING_TENANT_SCOPE"
	CodeInsufficientScope   ErrorCode = "INSUFFICIENT_SCOPE"
	CodeFeatureNotAvailable ErrorCode = "FEATURE_NOT_AVAILABLE"
	CodeProductPaused       ErrorCode = "PRODUCT_PAUSED"
	CodeRateLimitExceeded   ErrorCode = "RATE_LIMIT_EXCEEDED"
	CodeInternalError       ErrorCode = "INTERNAL_ERROR"

	// ── вход ─────────────────────────────────────────────────────────────
	CodeInvalidInput       ErrorCode = "INVALID_INPUT"
	CodePIINotAccepted     ErrorCode = "PII_NOT_ACCEPTED"
	CodeReservedExternalID ErrorCode = "RESERVED_EXTERNAL_ID"

	// ── чего не нашли ────────────────────────────────────────────────────
	CodePartnerNotFound    ErrorCode = "PARTNER_NOT_FOUND"
	CodePartnerNotActive   ErrorCode = "PARTNER_NOT_ACTIVE"
	CodeAdvertiserNotFound ErrorCode = "ADVERTISER_NOT_FOUND"
	CodeOfferNotFound      ErrorCode = "OFFER_NOT_FOUND"
	CodeConversionNotFound ErrorCode = "CONVERSION_NOT_FOUND"
	CodePayoutNotFound     ErrorCode = "PAYOUT_NOT_FOUND"

	// ── денежные отказы ──────────────────────────────────────────────────
	CodeUnknownGoal         ErrorCode = "UNKNOWN_GOAL"
	CodeWindowExpired       ErrorCode = "WINDOW_EXPIRED"
	CodeAlreadyConverted    ErrorCode = "ALREADY_CONVERTED"
	CodeExternalIDConflict  ErrorCode = "EXTERNAL_ID_CONFLICT"
	CodeIllegalTransition   ErrorCode = "ILLEGAL_TRANSITION"
	CodeIdempotentMismatch  ErrorCode = "IDEMPOTENT_MISMATCH"
	CodeBelowMinPayout      ErrorCode = "BELOW_MIN_PAYOUT"
	CodeInsufficientBalance ErrorCode = "INSUFFICIENT_BALANCE"
	CodeReversalFailed      ErrorCode = "REVERSAL_FAILED"

	// CodeMalformedResponse ставит САМ клиент, когда дверь ответила не по
	// контракту: 2xx с success:false, отказ без конверта. Дверь такого кода не
	// присылает — потому его и нет в реестре Recca.
	//
	// ⚠ Подставлять сюда статус нельзя: код вида HTTP_200 в журнале
	// потребителя выглядел бы как подтверждение успеха.
	CodeMalformedResponse ErrorCode = "MALFORMED_RESPONSE"
)

// Сентинелы. У каждого кода свой — это и есть обещанный способ ветвиться через
// errors.Is, не сравнивая строки.
var (
	ErrMissingTenantScope  = errors.New("cpa: ключ арендатора не предъявлен или отозван")
	ErrInsufficientScope   = errors.New("cpa: у ключа нет права на этот метод")
	ErrFeatureNotAvailable = errors.New("cpa: продукт «CPA-сеть» не подключён")
	ErrProductPaused       = errors.New("cpa: продукт «CPA-сеть» приостановлен")
	ErrRateLimited         = errors.New("cpa: исчерпан бюджет запросов ключа")
	ErrInternal            = errors.New("cpa: сбой на стороне Recca")

	ErrInvalidInput       = errors.New("cpa: тело или параметры не прошли проверку двери")
	ErrPIINotAccepted     = errors.New("cpa: в свободном поле распознано персональное данное")
	ErrReservedExternalID = errors.New("cpa: external_id из зарезервированного пространства платформы")

	ErrPartnerNotFound    = errors.New("cpa: партнёра с таким external_id в сети нет")
	ErrPartnerNotActive   = errors.New("cpa: партнёр сейчас работать не может")
	ErrAdvertiserNotFound = errors.New("cpa: рекламодателя с таким advertiser_id в сети нет")
	ErrOfferNotFound      = errors.New("cpa: оффера с таким идентификатором нет")
	ErrConversionNotFound = errors.New("cpa: конверсии с таким external_id и целью нет")
	ErrPayoutNotFound     = errors.New("cpa: заявки на выплату с таким идентификатором нет")

	ErrUnknownGoal         = errors.New("cpa: у оффера нет такой цели")
	ErrWindowExpired       = errors.New("cpa: окно атрибуции оффера истекло")
	ErrAlreadyConverted    = errors.New("cpa: по этому переходу уже засчитана конверсия на ту же цель")
	ErrExternalIDConflict  = errors.New("cpa: external_id занят в сети другим оффером по той же цели")
	ErrIllegalTransition   = errors.New("cpa: такой переход статуса невозможен")
	ErrIdempotentMismatch  = errors.New("cpa: операция с этим ключом идемпотентности была с другими параметрами")
	ErrBelowMinPayout      = errors.New("cpa: сумма меньше минимальной выплаты сети")
	ErrInsufficientBalance = errors.New("cpa: доступного остатка партнёра не хватает")
	ErrReversalFailed      = errors.New("cpa: возврат начался, но не завершился")

	ErrMalformedResponse = errors.New("cpa: дверь ответила не по контракту")
)

// codeInfo — что клиент знает о коде: с каким HTTP он приезжает и переживает ли
// повтор сам по себе.
//
// ⚠ Повторяемость КОДА и повторяемость СТАТУСА — независимые признаки, и оба
// нужны: код может быть новым (его ещё нет в реестре), а 5xx и 429 говорят о
// сбое сами. Сводить их в один признак значило бы потерять один из двух.
type codeInfo struct {
	http      int
	sentinel  error
	retryable bool
}

var codeRegistry = map[ErrorCode]codeInfo{
	CodeMissingTenantScope:  {http.StatusUnauthorized, ErrMissingTenantScope, false},
	CodeInsufficientScope:   {http.StatusForbidden, ErrInsufficientScope, false},
	CodeFeatureNotAvailable: {http.StatusForbidden, ErrFeatureNotAvailable, false},
	CodeProductPaused:       {http.StatusForbidden, ErrProductPaused, false},
	CodeRateLimitExceeded:   {http.StatusTooManyRequests, ErrRateLimited, true},
	CodeInternalError:       {http.StatusInternalServerError, ErrInternal, true},

	CodeInvalidInput:       {http.StatusBadRequest, ErrInvalidInput, false},
	CodePIINotAccepted:     {http.StatusBadRequest, ErrPIINotAccepted, false},
	CodeReservedExternalID: {http.StatusBadRequest, ErrReservedExternalID, false},

	CodePartnerNotFound:    {http.StatusNotFound, ErrPartnerNotFound, false},
	CodePartnerNotActive:   {http.StatusConflict, ErrPartnerNotActive, false},
	CodeAdvertiserNotFound: {http.StatusNotFound, ErrAdvertiserNotFound, false},
	CodeOfferNotFound:      {http.StatusNotFound, ErrOfferNotFound, false},
	CodeConversionNotFound: {http.StatusNotFound, ErrConversionNotFound, false},
	CodePayoutNotFound:     {http.StatusNotFound, ErrPayoutNotFound, false},

	CodeUnknownGoal:         {http.StatusBadRequest, ErrUnknownGoal, false},
	CodeWindowExpired:       {http.StatusConflict, ErrWindowExpired, false},
	CodeAlreadyConverted:    {http.StatusConflict, ErrAlreadyConverted, false},
	CodeExternalIDConflict:  {http.StatusConflict, ErrExternalIDConflict, false},
	CodeIllegalTransition:   {http.StatusConflict, ErrIllegalTransition, false},
	CodeIdempotentMismatch:  {http.StatusConflict, ErrIdempotentMismatch, false},
	CodeBelowMinPayout:      {http.StatusBadRequest, ErrBelowMinPayout, false},
	CodeInsufficientBalance: {http.StatusConflict, ErrInsufficientBalance, false},
	CodeReversalFailed:      {http.StatusInternalServerError, ErrReversalFailed, true},

	// Код клиента, а не двери: HTTP у него тот, с которым пришёл негодный
	// ответ, поэтому здесь ноль — «статуса своего нет».
	CodeMalformedResponse: {0, ErrMalformedResponse, false},
}

// Codes перечисляет коды, известные клиенту, в устойчивом порядке.
func Codes() []ErrorCode {
	out := make([]ErrorCode, 0, len(codeRegistry))
	for code := range codeRegistry {
		out = append(out, code)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// HTTPStatusFor называет статус, с которым код приезжает по контракту. Второе
// значение — знаком ли код клиенту вообще: новый код на новом условии
// считается совместимой правкой (раздел 15), и клиент обязан его пережить.
func HTTPStatusFor(code ErrorCode) (int, bool) {
	info, ok := codeRegistry[code]
	return info.http, ok
}

// SentinelFor возвращает сентинел кода или nil, если код клиенту неизвестен.
func SentinelFor(code ErrorCode) error {
	if info, ok := codeRegistry[code]; ok {
		return info.sentinel
	}
	return nil
}

// Retryable отвечает, стоит ли повторять запрос, получивший этот отказ.
//
// ⚠⚠ Это утверждение об ОТКАЗЕ, а не разрешение повторять ОПЕРАЦИЮ. Повтор
// делает транспорт и только для запросов, объявленных идемпотентными: повтор
// неидемпотентного POST оплатил бы конверсию дважды.
func Retryable(code ErrorCode, status int) bool {
	if status >= http.StatusInternalServerError || status == http.StatusTooManyRequests {
		return true
	}
	if info, ok := codeRegistry[code]; ok {
		return info.retryable
	}
	return false
}

// APIError — отказ двери: код, сообщение, статус и идентификатор запроса.
//
// Идентификатор — поле, а не строка в журнале SDK: по нему нас просят искать
// цепочку на своей стороне, и потребителю он нужен в тикете.
type APIError struct {
	// Code — код контракта; сравнивать его лучше через errors.Is с сентинелом.
	Code ErrorCode
	// Message — диагностика для человека. Частью контракта не является.
	Message string
	// HTTPStatus — статус ответа, с которым отказ приехал.
	HTTPStatus int
	// RequestID — meta.request_id либо заголовок X-Request-Id.
	RequestID string
	// Details — поле «details» конверта, как прислала дверь: на отказах про
	// вход оно называет поле поимённо.
	Details json.RawMessage
	// Retryable — переживает ли этот отказ повтор (см. Retryable).
	Retryable bool
}

func (e *APIError) Error() string {
	if e.RequestID != "" {
		return fmt.Sprintf("cpa: %s (HTTP %d, request_id %s): %s", e.Code, e.HTTPStatus, e.RequestID, e.Message)
	}
	return fmt.Sprintf("cpa: %s (HTTP %d): %s", e.Code, e.HTTPStatus, e.Message)
}

// Unwrap выдаёт сентинел кода, чтобы errors.Is находил его без сравнения строк.
// Для неизвестного кода — nil: иначе ошибка совпала бы с чужим сентинелом и
// потребитель увидел бы не ту причину.
func (e *APIError) Unwrap() error { return SentinelFor(e.Code) }

// ErrValidation — общий признак отказа, который клиент дал САМ, не обращаясь к
// двери. Отличать его от ответа Recca необходимо: иначе интегратор искал бы
// свой request_id в наших журналах.
var ErrValidation = errors.New("cpa: запрос не прошёл проверку до отправки")

// ValidationError называет поле и причину.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("cpa: поле %s не прошло проверку до отправки: %s", e.Field, e.Reason)
}

func (e *ValidationError) Unwrap() error { return ErrValidation }

// invalid — короткая форма для проверок ниже.
func invalid(field, reason string) error {
	return &ValidationError{Field: field, Reason: reason}
}
