package cpa

import (
	"context"
	"net/http"
)

const statisticsPath = "/cpa/statistics"

// StatisticsService — отчёт по кликам, конверсиям и выплатам.
type StatisticsService service

// Get читает отчёт: GET /cpa/statistics.
//
// ⚠ Отчёт считается по `occurred_at ?? created_at`, а не по времени приёма:
// досланная пачкой вчерашняя конверсия попадает во вчерашний день. Сверять с
// собственными числами надо по тому же правилу, иначе расхождение будет
// объясняться не данными, а часовым сдвигом досылки.
//
// ⚠ GroupBy обязателен: разреза по умолчанию у отчёта нет, и подставить его за
// интегратора значило бы ответить не на заданный вопрос.
func (s *StatisticsService) Get(ctx context.Context, params StatisticsParams) ([]StatisticsRow, *Response, error) {
	if err := params.Validate(); err != nil {
		return nil, nil, err
	}

	return doList[StatisticsRow](ctx, (*service)(s), request{
		Method:  http.MethodGet,
		Path:    statisticsPath,
		Query:   params.Query(),
		Subject: "отчёт",
	})
}
