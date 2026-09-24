package cpa

import "go.recca.ru/cpa/cpatypes"

// Ошибки двери переэкспортированы из cpatypes, чтобы квикстарту хватало одного
// импорта. Значения те же самые — это алиасы, а не вторые определения:
// errors.Is и errors.As работают одинаково, как ни назови.
//
// ⚠ Добавили код в cpatypes — добавьте строку и сюда. Компилятор об этом не
// напомнит: отсутствие имени здесь ничего не ломает, оно просто делает код
// недоступным под коротким именем.

type (
	// ErrorCode — код отказа двери.
	ErrorCode = cpatypes.ErrorCode
	// APIError — отказ двери: код, сообщение, статус, request_id.
	APIError = cpatypes.APIError
	// ValidationError — отказ, который клиент дал сам, не обращаясь к двери.
	ValidationError = cpatypes.ValidationError
)

const (
	CodeMissingTenantScope  = cpatypes.CodeMissingTenantScope
	CodeInsufficientScope   = cpatypes.CodeInsufficientScope
	CodeFeatureNotAvailable = cpatypes.CodeFeatureNotAvailable
	CodeProductPaused       = cpatypes.CodeProductPaused
	CodeRateLimitExceeded   = cpatypes.CodeRateLimitExceeded
	CodeInternalError       = cpatypes.CodeInternalError

	CodeInvalidInput       = cpatypes.CodeInvalidInput
	CodePIINotAccepted     = cpatypes.CodePIINotAccepted
	CodeReservedExternalID = cpatypes.CodeReservedExternalID

	CodePartnerNotFound    = cpatypes.CodePartnerNotFound
	CodePartnerNotActive   = cpatypes.CodePartnerNotActive
	CodeAdvertiserNotFound = cpatypes.CodeAdvertiserNotFound
	CodeOfferNotFound      = cpatypes.CodeOfferNotFound
	CodeConversionNotFound = cpatypes.CodeConversionNotFound
	CodePayoutNotFound     = cpatypes.CodePayoutNotFound

	CodeUnknownGoal         = cpatypes.CodeUnknownGoal
	CodeWindowExpired       = cpatypes.CodeWindowExpired
	CodeAlreadyConverted    = cpatypes.CodeAlreadyConverted
	CodeExternalIDConflict  = cpatypes.CodeExternalIDConflict
	CodeIllegalTransition   = cpatypes.CodeIllegalTransition
	CodeIdempotentMismatch  = cpatypes.CodeIdempotentMismatch
	CodeBelowMinPayout      = cpatypes.CodeBelowMinPayout
	CodeInsufficientBalance = cpatypes.CodeInsufficientBalance
	CodeReversalFailed      = cpatypes.CodeReversalFailed

	CodeMalformedResponse = cpatypes.CodeMalformedResponse
)

// Сентинелы: errors.Is(err, cpa.ErrWindowExpired) вместо сравнения строк.
var (
	ErrMissingTenantScope  = cpatypes.ErrMissingTenantScope
	ErrInsufficientScope   = cpatypes.ErrInsufficientScope
	ErrFeatureNotAvailable = cpatypes.ErrFeatureNotAvailable
	ErrProductPaused       = cpatypes.ErrProductPaused
	ErrRateLimited         = cpatypes.ErrRateLimited
	ErrInternal            = cpatypes.ErrInternal

	ErrInvalidInput       = cpatypes.ErrInvalidInput
	ErrPIINotAccepted     = cpatypes.ErrPIINotAccepted
	ErrReservedExternalID = cpatypes.ErrReservedExternalID

	ErrPartnerNotFound    = cpatypes.ErrPartnerNotFound
	ErrPartnerNotActive   = cpatypes.ErrPartnerNotActive
	ErrAdvertiserNotFound = cpatypes.ErrAdvertiserNotFound
	ErrOfferNotFound      = cpatypes.ErrOfferNotFound
	ErrConversionNotFound = cpatypes.ErrConversionNotFound
	ErrPayoutNotFound     = cpatypes.ErrPayoutNotFound

	ErrUnknownGoal         = cpatypes.ErrUnknownGoal
	ErrWindowExpired       = cpatypes.ErrWindowExpired
	ErrAlreadyConverted    = cpatypes.ErrAlreadyConverted
	ErrExternalIDConflict  = cpatypes.ErrExternalIDConflict
	ErrIllegalTransition   = cpatypes.ErrIllegalTransition
	ErrIdempotentMismatch  = cpatypes.ErrIdempotentMismatch
	ErrBelowMinPayout      = cpatypes.ErrBelowMinPayout
	ErrInsufficientBalance = cpatypes.ErrInsufficientBalance
	ErrReversalFailed      = cpatypes.ErrReversalFailed

	ErrMalformedResponse = cpatypes.ErrMalformedResponse

	// ErrValidation — общий признак отказа до сети. ⚠ Отличать его от ответа
	// Recca необходимо: иначе интегратор искал бы свой request_id в наших
	// журналах.
	ErrValidation = cpatypes.ErrValidation
	// ErrIncompleteData — успешный ответ без полного набора полей.
	ErrIncompleteData = cpatypes.ErrIncompleteData
)

// Codes перечисляет коды, известные клиенту.
func Codes() []ErrorCode { return cpatypes.Codes() }

// HTTPStatusFor называет статус кода по контракту; второе значение — знаком ли
// код клиенту вообще.
func HTTPStatusFor(code ErrorCode) (int, bool) { return cpatypes.HTTPStatusFor(code) }

// SentinelFor возвращает сентинел кода или nil для неизвестного.
func SentinelFor(code ErrorCode) error { return cpatypes.SentinelFor(code) }

// Retryable отвечает, стоит ли повторять запрос, получивший такой отказ.
//
// ⚠ Это утверждение об отказе, а не разрешение повторять операцию: повтор
// неидемпотентного POST оплатил бы конверсию дважды.
func Retryable(code ErrorCode, status int) bool { return cpatypes.Retryable(code, status) }
