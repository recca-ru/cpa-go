package cpa

import (
	"context"
	"net/http"
)

const partnersPath = "/cpa/partners"

// PartnersService — регистрация партнёров, их карточки и балансы.
type PartnersService service

// Upsert заводит партнёра или правит его: POST /cpa/partners.
//
// Ответ приезжает с 201, когда партнёр создан, и с 200 на повторе — **и то и
// другое успех**, ровно как у приёма конверсии.
//
// Повтор меняет ТОЛЬКО поля интегратора — участие (PartnerState) и рекрутёра.
// Санкцию оператора он не трогает.
//
// ⚠⚠ ЛОВУШКА СИНХРОНИЗАЦИИ. Роль и ставку агента на уже заведённом партнёре меняет
// ОПЕРАТОР в кабинете, и дверь их НЕ принимает молча: повтор с другой Role — 400
// INVALID_INPUT с полем role; повтор с другим AgentPercent у действующего агента —
// 400 INVALID_INPUT с полем agent_percent. Отказ, а не тихое применение, потому
// что применить значило бы откатывать решение оператора каждой ночной
// синхронизацией, а проглотить — оставить вас в уверенности, что вы поменяли то,
// чего не меняли.
//
// Практически: синхронизация, которая шлёт роль СО СВОЕЙ стороны, начнёт падать на
// партнёре, которого оператор перевёл в агенты, — и будет падать на нём каждую ночь.
// Сверяйте роль с ответом Partners.Get либо не пересылайте её для уже заведённых.
// ⚠ У приостановленного партнёра роли нет, и с ним не сверяется ни роль, ни ставка —
// поэтому поток отказов начнётся не сразу, а после того, как его вернут в строй.
//
// ⚠ Операция идемпотентна по external_id, поэтому её POST объявлен повторяемым.
func (s *PartnersService) Upsert(ctx context.Context, params UpsertPartnerParams) (*Partner, *Response, error) {
	if err := params.Validate(); err != nil {
		return nil, nil, err
	}

	return do[Partner](ctx, (*service)(s), request{
		Method:          http.MethodPost,
		Path:            partnersPath,
		Body:            params,
		Subject:         "партнёр",
		IdempotentWrite: true,
	})
}

// List читает страницу партнёров: GET /cpa/partners.
//
// ⚠ Фильтр RecruiterExternalID и есть «мои блогеры» в кабинете агента. Неизвестный
// рекрутёр — 404 PARTNER_NOT_FOUND, а не пустая страница.
func (s *PartnersService) List(ctx context.Context, params ListPartnersParams) ([]Partner, *Response, error) {
	if err := params.Validate(); err != nil {
		return nil, nil, err
	}

	return doList[Partner](ctx, (*service)(s), request{
		Method:  http.MethodGet,
		Path:    partnersPath,
		Query:   params.Query(),
		Subject: "страница партнёров",
	})
}

// Get читает карточку партнёра: GET /cpa/partners/{external_id}.
func (s *PartnersService) Get(ctx context.Context, externalID PartnerExternalID) (*Partner, *Response, error) {
	if err := externalID.Validate(); err != nil {
		return nil, nil, err
	}

	return do[Partner](ctx, (*service)(s), request{
		Method:  http.MethodGet,
		Path:    partnersPath + "/" + pathSegment(string(externalID)),
		Subject: "партнёр",
	})
}

// Balance читает баланс партнёра: GET /cpa/partners/{external_id}/balance.
//
// ⚠⚠ MinPayoutRUB брать ОТСЮДА, а не из своей константы: порог принадлежит сети и
// меняется оператором в кабинете. CanRequest — ответ двери на вопрос «хватит ли на
// заявку», и он учитывает порог, а не только доступную сумму.
//
// ⚠ OnHoldRUB станет доступным по истечении холда конверсий само: отдельного
// запроса на это нет и не нужно.
func (s *PartnersService) Balance(ctx context.Context, externalID PartnerExternalID) (*PartnerBalance, *Response, error) {
	if err := externalID.Validate(); err != nil {
		return nil, nil, err
	}

	return do[PartnerBalance](ctx, (*service)(s), request{
		Method:  http.MethodGet,
		Path:    partnersPath + "/" + pathSegment(string(externalID)) + "/balance",
		Subject: "баланс партнёра",
	})
}
