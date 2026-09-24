package cpa

import (
	"time"

	"go.recca.ru/cpa/cpatypes"
)

// Типы контракта под короткими именами: квикстарту и обычному вызову хватает
// одного импорта, `go.recca.ru/cpa`.
//
// ⚠ Это ПСЕВДОНИМЫ, а не вторые определения: значение то же самое, и структура,
// собранная под именем cpatypes.Partner, ложится в метод, объявленный с cpa.Partner.
//
// ⚠ Добавили тип в cpatypes — добавьте строку и сюда. Компилятор об этом не
// напомнит: отсутствие имени здесь ничего не ломает, оно просто делает тип
// недоступным под коротким именем. Держит это `TestShortNamesCoverContractTypes`.

type (
	// Meta — блок meta конверта ответа.
	Meta = cpatypes.Meta
	// Response — статус, request_id, meta и заголовки ответа.
	Response = cpatypes.Response
	// PageParams — общая часть параметров страницы.
	PageParams = cpatypes.PageParams
	// Date — календарная дата без времени: период выплаты, границы отчёта.
	Date = cpatypes.Date

	// SelfInfo — ответ GET /cpa/self.
	SelfInfo = cpatypes.SelfInfo
	// DeploymentEnvironment — среда, которую называет сервер.
	DeploymentEnvironment = cpatypes.DeploymentEnvironment
	// AgentHold— когда дозревает нога рекрутёра.
	AgentHold = cpatypes.AgentHold
	// AgentHoldMode — по какому сроку она дозревает.
	AgentHoldMode = cpatypes.AgentHoldMode
	// AgentLegBase — от чего считается нога рекрутёра.
	AgentLegBase = cpatypes.AgentLegBase
	// AgentLeg — нога рекрутёра в карточке конверсии.
	AgentLeg = cpatypes.AgentLeg
	// WebhookConfig — настройка адреса событий.
	WebhookConfig = cpatypes.WebhookConfig
	// WebhookEvent — событие cpa.*.
	WebhookEvent = cpatypes.WebhookEvent
	// SetWebhookParams — вход PUT /cpa/self/webhook.
	SetWebhookParams = cpatypes.SetWebhookParams

	// Partner — карточка партнёра.
	Partner = cpatypes.Partner
	// PartnerRole — роль партнёра в сети.
	PartnerRole = cpatypes.PartnerRole
	// PartnerState — участие, которым распоряжается интегратор.
	PartnerState = cpatypes.PartnerState
	// PartnerStatus — санкция, которой распоряжается оператор.
	PartnerStatus = cpatypes.PartnerStatus
	// PartnerBalance — доступно, в холде, выплачено, порог.
	PartnerBalance = cpatypes.PartnerBalance
	// UpsertPartnerParams — вход POST /cpa/partners.
	UpsertPartnerParams = cpatypes.UpsertPartnerParams
	// ListPartnersParams — фильтры GET /cpa/partners.
	ListPartnersParams = cpatypes.ListPartnersParams

	// RateModel — модель оплаты оффера.
	RateModel = cpatypes.RateModel
	// RateKind — чем выражена ставка.
	RateKind = cpatypes.RateKind
	// Rate — ставка: рубли либо процент.
	Rate = cpatypes.Rate
	// GeoMode — как читать список стран.
	GeoMode = cpatypes.GeoMode
	// GeoRules — география оффера.
	GeoRules = cpatypes.GeoRules
	// OfferGoalPartner — цель оффера в партнёрской проекции.
	OfferGoalPartner = cpatypes.OfferGoalPartner
	// OfferGoalAdvertiser — она же в проекции рекламодателя.
	OfferGoalAdvertiser = cpatypes.OfferGoalAdvertiser
	// OfferPartnerView — оффер, каким его видит блогер.
	OfferPartnerView = cpatypes.OfferPartnerView
	// OfferAdvertiserView — оффер, каким его видит рекламодатель.
	OfferAdvertiserView = cpatypes.OfferAdvertiserView
	// CreativeKind — вид материала оффера.
	CreativeKind = cpatypes.CreativeKind
	// Creative — материал оффера.
	Creative = cpatypes.Creative
	// ListOffersParams — фильтры GET /cpa/offers.
	ListOffersParams = cpatypes.ListOffersParams

	// Link — стабильная ссылка партнёра на оффер.
	Link = cpatypes.Link
	// CreateLinkParams — вход POST /cpa/links.
	CreateLinkParams = cpatypes.CreateLinkParams

	// Click — переход.
	Click = cpatypes.Click
	// ClickStatus — что стало с переходом.
	ClickStatus = cpatypes.ClickStatus
	// CreateClickParams — вход POST /cpa/clicks.
	CreateClickParams = cpatypes.CreateClickParams

	// Conversion — конверсия в проекции списка.
	Conversion = cpatypes.Conversion
	// ConversionDetail — карточка конверсии: тройка ставок и нога рекрутёра.
	ConversionDetail = cpatypes.ConversionDetail
	// ConversionStatus — состояние конверсии.
	ConversionStatus = cpatypes.ConversionStatus
	// CreateConversionParams — вход POST /cpa/conversions.
	CreateConversionParams = cpatypes.CreateConversionParams
	// ListConversionsParams — фильтры GET /cpa/conversions.
	ListConversionsParams = cpatypes.ListConversionsParams
	// GetConversionParams — адресация карточки конверсии.
	GetConversionParams = cpatypes.GetConversionParams
	// CancelConversionParams — вход отмены конверсии.
	CancelConversionParams = cpatypes.CancelConversionParams

	// Payout — заявка на выплату.
	Payout = cpatypes.Payout
	// PayoutStatus — состояние заявки.
	PayoutStatus = cpatypes.PayoutStatus
	// PayoutParams — вход POST /cpa/payouts; собирается NewPayoutParams.
	PayoutParams = cpatypes.PayoutParams
	// ListPayoutsParams — фильтры GET /cpa/payouts.
	ListPayoutsParams = cpatypes.ListPayoutsParams

	// StatisticsGroupBy — разрез отчёта.
	StatisticsGroupBy = cpatypes.StatisticsGroupBy
	// StatisticsRow — строка отчёта.
	StatisticsRow = cpatypes.StatisticsRow
	// StatisticsParams — параметры GET /cpa/statistics.
	StatisticsParams = cpatypes.StatisticsParams
)

// Словари контракта.
const (
	DeploymentProduction = cpatypes.DeploymentProduction
	DeploymentStaging    = cpatypes.DeploymentStaging
	DeploymentSandbox    = cpatypes.DeploymentSandbox

	AgentHoldFollowConversion = cpatypes.AgentHoldFollowConversion
	AgentHoldOwnDays          = cpatypes.AgentHoldOwnDays

	AgentLegFromMargin = cpatypes.AgentLegFromMargin
	AgentLegFromPayout = cpatypes.AgentLegFromPayout

	WebhookConversionApproved = cpatypes.WebhookConversionApproved
	WebhookConversionDeclined = cpatypes.WebhookConversionDeclined
	WebhookConversionReversed = cpatypes.WebhookConversionReversed
	WebhookPayoutPaid         = cpatypes.WebhookPayoutPaid

	PartnerWebmaster  = cpatypes.PartnerWebmaster
	PartnerAgent      = cpatypes.PartnerAgent
	PartnerAdvertiser = cpatypes.PartnerAdvertiser

	PartnerStateActive   = cpatypes.PartnerStateActive
	PartnerStateInactive = cpatypes.PartnerStateInactive

	PartnerStatusActive    = cpatypes.PartnerStatusActive
	PartnerStatusPending   = cpatypes.PartnerStatusPending
	PartnerStatusSuspended = cpatypes.PartnerStatusSuspended

	RateCPA      = cpatypes.RateCPA
	RateCPL      = cpatypes.RateCPL
	RateRevShare = cpatypes.RateRevShare

	RateFixed   = cpatypes.RateFixed
	RatePercent = cpatypes.RatePercent

	GeoAny   = cpatypes.GeoAny
	GeoAllow = cpatypes.GeoAllow
	GeoDeny  = cpatypes.GeoDeny

	CreativeBanner  = cpatypes.CreativeBanner
	CreativeLanding = cpatypes.CreativeLanding
	CreativeText    = cpatypes.CreativeText
	CreativeVideo   = cpatypes.CreativeVideo
	CreativeOther   = cpatypes.CreativeOther

	ClickOpen        = cpatypes.ClickOpen
	ClickTrafficback = cpatypes.ClickTrafficback

	ConversionPending  = cpatypes.ConversionPending
	ConversionHold     = cpatypes.ConversionHold
	ConversionApproved = cpatypes.ConversionApproved
	ConversionDeclined = cpatypes.ConversionDeclined
	ConversionNotFound = cpatypes.ConversionNotFound

	PayoutRequested = cpatypes.PayoutRequested
	PayoutApproved  = cpatypes.PayoutApproved
	PayoutPaid      = cpatypes.PayoutPaid
	PayoutRejected  = cpatypes.PayoutRejected

	StatisticsByOffer   = cpatypes.StatisticsByOffer
	StatisticsByPartner = cpatypes.StatisticsByPartner
	StatisticsByDay     = cpatypes.StatisticsByDay
)

// NewPayoutParams собирает заявку на выплату целиком либо не собирает вовсе.
//
// ⚠⚠ Конструктор, а не структурный литерал: личность заявки — партнёр и период,
// и заявка без окна попала бы в ту же строку, что заявка другого месяца. Сумма в
// ключ не входит — она сверяется: другая сумма за тот же период даёт 409
// IDEMPOTENT_MISMATCH.
func NewPayoutParams(partner PartnerExternalID, amount RUB, from, to Date) (PayoutParams, error) {
	return cpatypes.NewPayoutParams(partner, amount, from, to)
}

// NewDate собирает календарную дату в UTC.
func NewDate(year int, month time.Month, day int) Date { return cpatypes.NewDate(year, month, day) }

// DateOf берёт календарную дату момента в его же зоне.
func DateOf(t time.Time) Date { return cpatypes.DateOf(t) }

// ParseDate разбирает дату контрактной формы (2006-01-02).
func ParseDate(s string) (Date, error) { return cpatypes.ParseDate(s) }
