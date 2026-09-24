package cpatypes

import (
	"net/url"
	"strconv"
)

// StatisticsGroupBy — разрез отчёта.
type StatisticsGroupBy string

const (
	StatisticsByOffer   StatisticsGroupBy = "offer"
	StatisticsByPartner StatisticsGroupBy = "partner"
	StatisticsByDay     StatisticsGroupBy = "day"
)

// StatisticsRow — строка отчёта.
type StatisticsRow struct {
	// Key — идентификатор группы: оффер, партнёр или дата.
	Key         string `json:"key" cpa:"required"`
	Clicks      int    `json:"clicks" cpa:"required"`
	Conversions int    `json:"conversions" cpa:"required"`
	// Approved — сколько из них подтверждено. Дверь считает его всегда, поэтому
	// ноль значит «ни одной подтверждённой», а не «не считали»; отсутствие поля —
	// ErrIncompleteData.
	Approved  int `json:"approved" cpa:"required"`
	PayoutRUB RUB `json:"payout_rub" cpa:"required"`
	// RevenueRUB — выручка группы. Ключ этой двери принадлежит оператору сети, и
	// выручка приходит в каждой строке (контракт 2026-09-24: обязательна, не
	// nullable).
	RevenueRUB RUB `json:"revenue_rub" cpa:"required"`
}

// StatisticsParams — параметры GET /cpa/statistics.
//
// ⚠ Отчёт считается по `occurred_at ?? created_at`: досланная пачкой вчерашняя
// конверсия попадает во вчерашний день, а не в сегодняшний.
type StatisticsParams struct {
	// GroupBy обязателен: разреза по умолчанию у отчёта нет, и выдумывать его за
	// интегратора значило бы отвечать не на заданный вопрос.
	GroupBy           StatisticsGroupBy
	PartnerExternalID PartnerExternalID
	AdvertiserID      string
	OfferID           string
	// From и To — календарные ДАТЫ, а не моменты: у этой двери контракт объявляет
	// `date`. У выборки конверсий наоборот — полное время.
	From *Date
	To   *Date
}

// Query собирает строку запроса.
func (p StatisticsParams) Query() url.Values {
	q := url.Values{}
	q.Set("group_by", string(p.GroupBy))
	setIfNotEmpty(q, "partner_external_id", string(p.PartnerExternalID))
	setIfNotEmpty(q, "advertiser_id", p.AdvertiserID)
	setIfNotEmpty(q, "offer_id", p.OfferID)
	if p.From != nil {
		q.Set("from", p.From.String())
	}
	if p.To != nil {
		q.Set("to", p.To.String())
	}
	return q
}

// Validate проверяет форму до отправки.
func (p StatisticsParams) Validate() error {
	switch p.GroupBy {
	case StatisticsByOffer, StatisticsByPartner, StatisticsByDay:
	case "":
		return invalid("group_by", "обязательное поле: offer, partner или day")
	default:
		return invalid("group_by", "неизвестный разрез "+strconv.Quote(string(p.GroupBy))+": допустимы offer, partner и day")
	}
	if p.PartnerExternalID != "" {
		if err := p.PartnerExternalID.Validate(); err != nil {
			return err
		}
	}
	if p.From != nil && p.To != nil && p.To.Before(p.From.Time) {
		return invalid("to", "конец периода раньше начала: "+p.From.String()+" … "+p.To.String())
	}
	return nil
}
