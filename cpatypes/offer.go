package cpatypes

import (
	"encoding/json"
	"net/url"
	"time"
)

// RateModel — модель оплаты оффера.
type RateModel string

const (
	RateCPA      RateModel = "cpa"
	RateCPL      RateModel = "cpl"
	RateRevShare RateModel = "revshare"
)

// RateKind — чем выражена ставка.
type RateKind string

const (
	RateFixed   RateKind = "fixed"
	RatePercent RateKind = "percent"
)

// Rate — ставка: либо рубли, либо процент.
//
// ⚠ Оба значения указатели, и заполнено ровно одно — то, которое соответствует
// Kind. Сложить их в одно число нельзя: 500 ₽ и 500 % — разные обещания, а по
// нулю на месте второго их не различить.
type Rate struct {
	Kind RateKind `json:"kind" cpa:"required"`
	// ValueRUB — при RateFixed.
	ValueRUB *RUB `json:"value_rub"`
	// ValuePercent — при RatePercent. Лексическая форма: доля бывает дробной
	// (1.5 %), и float64 на деньгах даёт ошибку, которой не видно до кассы.
	ValuePercent *json.Number `json:"value_percent"`
}

// GeoMode — как читать список стран.
type GeoMode string

const (
	// GeoAny — весь мир; список тогда пуст.
	GeoAny GeoMode = "any"
	// GeoAllow — работаем ТОЛЬКО в перечисленных.
	GeoAllow GeoMode = "allow"
	// GeoDeny — в перечисленных НЕ работаем.
	GeoDeny GeoMode = "deny"
)

// GeoRules — география оффера.
//
// ⚠⚠ Режим, а не просто список. Плоский перечень стран неразличим между
// «работаем только здесь» и «здесь не работаем», и прочитав deny как allow,
// интегратор полил бы трафик ровно туда, откуда его отбивают, — молча и за свои
// деньги.
type GeoRules struct {
	Mode      GeoMode  `json:"mode" cpa:"required"`
	Countries []string `json:"countries" cpa:"required"`
}

// OfferGoalPartner — цель оффера в партнёрской проекции.
type OfferGoalPartner struct {
	Code string `json:"code" cpa:"required"`
	Name string `json:"name" cpa:"required"`
	// IsMilestone — веха: цель без ставок («дошёл до вебинара»). За неё не
	// платят, и PayoutRate у неё null.
	IsMilestone bool  `json:"is_milestone" cpa:"required"`
	PayoutRate  *Rate `json:"payout_rate"`
	HoldDays    *int  `json:"hold_days"`
}

// OfferGoalAdvertiser — та же цель в проекции рекламодателя.
//
// ⚠ Тип отдельный, а не общий с полем «ставка»: общий объект «цель со ставками»
// показал бы каждой стороне маржу оператора вычитанием.
type OfferGoalAdvertiser struct {
	Code        string `json:"code" cpa:"required"`
	Name        string `json:"name" cpa:"required"`
	IsMilestone bool   `json:"is_milestone" cpa:"required"`
	RevenueRate *Rate  `json:"revenue_rate"`
	HoldDays    *int   `json:"hold_days"`
}

// OfferPartnerView — оффер в партнёрской проекции: то, что видит блогер.
//
// ⚠⚠ revenue_rate, маржа, target_cpl_rub и капы здесь ОТСУТСТВУЮТ, и это
// свойство контракта, а не пропуск типа. Партнёр, увидевший ставку
// рекламодателя, вычисляет маржу оператора; капы — обещание о деньгах, данное
// рекламодателю.
type OfferPartnerView struct {
	ID   string `json:"id" cpa:"required"`
	Name string `json:"name" cpa:"required"`
	// AdvertiserID — НАШ идентификатор рекламодателя; внешнего у него нет и быть
	// не может, карточку заводит оператор в кабинете.
	AdvertiserID          string             `json:"advertiser_id" cpa:"required"`
	PayoutModel           RateModel          `json:"payout_model" cpa:"required"`
	PayoutRate            Rate               `json:"payout_rate" cpa:"required"`
	Goals                 []OfferGoalPartner `json:"goals" cpa:"required"`
	Geo                   GeoRules           `json:"geo" cpa:"required"`
	AllowedTraffic        []string           `json:"allowed_traffic" cpa:"required"`
	DisallowedTraffic     []string           `json:"disallowed_traffic"`
	HoldDays              *int               `json:"hold_days"`
	AttributionWindowDays *int               `json:"attribution_window_days"`
}

// OfferAdvertiserView — оффер в проекции рекламодателя: его собственная ставка.
//
// ⚠ payout_rate и маржа оператора здесь не отдаются никогда.
type OfferAdvertiserView struct {
	ID                    string                `json:"id" cpa:"required"`
	Name                  string                `json:"name" cpa:"required"`
	AdvertiserID          string                `json:"advertiser_id" cpa:"required"`
	PayoutModel           RateModel             `json:"payout_model" cpa:"required"`
	RevenueRate           Rate                  `json:"revenue_rate" cpa:"required"`
	Goals                 []OfferGoalAdvertiser `json:"goals"`
	Geo                   *GeoRules             `json:"geo"`
	HoldDays              *int                  `json:"hold_days"`
	AttributionWindowDays *int                  `json:"attribution_window_days"`
}

// CreativeKind — вид материала.
type CreativeKind string

const (
	CreativeBanner  CreativeKind = "banner"
	CreativeLanding CreativeKind = "landing"
	CreativeText    CreativeKind = "text"
	CreativeVideo   CreativeKind = "video"
	CreativeOther   CreativeKind = "other"
)

// Creative — материал оффера: баннер, посадочная, текст.
//
// ⚠ Метод отдаёт посадочные и креативы ОДНИМ списком, посадочные первыми: без
// ленда трафик вести некуда. Ветвиться — по Kind: у CreativeText заполнен Text,
// а URL там null по построению.
type Creative struct {
	ID          string       `json:"id" cpa:"required"`
	Kind        CreativeKind `json:"kind" cpa:"required"`
	Title       string       `json:"title" cpa:"required"`
	Description *string      `json:"description"`
	URL         *string      `json:"url"`
	Text        *string      `json:"text"`
	MimeType    *string      `json:"mime_type"`
	Width       *int         `json:"width"`
	Height      *int         `json:"height"`
	// IsDefault — основная посадочная. У креативов всегда false: понятие
	// принадлежит лендам.
	IsDefault *bool      `json:"is_default"`
	CreatedAt *time.Time `json:"created_at"`
}

// ListOffersParams — фильтры GET /cpa/offers.
//
// ⚠ Проекции здесь нет: её выбирает ИМЯ МЕТОДА (ListForPartner ⇆
// ListForAdvertiser). Параметром её пришлось бы проверять в рантайме, а именем
// её нельзя забыть вовсе.
type ListOffersParams struct {
	// AdvertiserID — сузить ПАРТНЁРСКИЙ каталог до одного рекламодателя.
	//
	// У проекции рекламодателя он не фильтр, а обязательное условие двери, и
	// поэтому там это аргумент метода ListForAdvertiser, а не это поле.
	//
	// ⚠ Неизвестный идентификатор — 404 ADVERTISER_NOT_FOUND, а не пустой
	// список: опечатка и «у рекламодателя нет офферов» — разные факты.
	AdvertiserID string
	PageParams
}

// Query собирает строку запроса. Проекцию дописывает вызывающий метод.
func (p ListOffersParams) Query() url.Values {
	q := url.Values{}
	setIfNotEmpty(q, "advertiser_id", p.AdvertiserID)
	p.PageParams.addTo(q)
	return q
}

// Validate проверяет то, в чём клиент прав наверняка.
func (p ListOffersParams) Validate() error { return p.PageParams.Validate() }
