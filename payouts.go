package cpa

import (
	"context"
	"net/http"
)

const payoutsPath = "/cpa/payouts"

// PayoutsService — заявки на выплату и их история.
//
// ⚠ Отметку «выплачено» ставит ОПЕРАТОР в кабинете: деньги уходят с его счёта, и
// подтверждает это человек. Машинная дверь заявку создаёт, но не закрывает — и
// метода «отметить выплаченной» здесь нет не по недосмотру.
type PayoutsService service

// Create создаёт заявку: POST /cpa/payouts.
//
// Успех — и 201 (заявка создана), и 200 (идемпотентный повтор той же заявки).
//
// ⚠⚠ Личность заявки — ПАРТНЁР И ПЕРИОД, а не сумма: за период у партнёра заявка
// ровно одна. Сумма в ключ не входит, она СВЕРЯЕТСЯ: повтор с той же суммой — та
// же заявка (200), с другой — 409 IDEMPOTENT_MISMATCH, а не вторая заявка и не
// молчаливый успех по прежней сумме. Другая сумма законна только за другой
// период. (Решение владельца 22.09; шапка utils/cpa-api-payouts.ts в Recca.)
//
// Собрать параметры можно только через NewPayoutParams — период входит в ключ, и
// заявка без него попала бы в ту же строку, что заявка другого месяца.
//
// Ключ в теле, поэтому POST объявлен повторяемым.
func (s *PayoutsService) Create(ctx context.Context, params PayoutParams) (*Payout, *Response, error) {
	if err := params.Validate(); err != nil {
		return nil, nil, err
	}

	return do[Payout](ctx, (*service)(s), request{
		Method:          http.MethodPost,
		Path:            payoutsPath,
		Body:            params,
		Subject:         "заявка на выплату",
		IdempotentWrite: true,
	})
}

// List читает историю заявок: GET /cpa/payouts.
func (s *PayoutsService) List(ctx context.Context, params ListPayoutsParams) ([]Payout, *Response, error) {
	if err := params.Validate(); err != nil {
		return nil, nil, err
	}

	return doList[Payout](ctx, (*service)(s), request{
		Method:  http.MethodGet,
		Path:    payoutsPath,
		Query:   params.Query(),
		Subject: "страница заявок на выплату",
	})
}
