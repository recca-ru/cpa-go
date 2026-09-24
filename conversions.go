package cpa

import (
	"context"
	"net/http"
)

// conversionsPath — путь метода приёма результата (от версии двери).
const conversionsPath = "/cpa/conversions"

// ConversionsService — приём и чтение конверсий.
type ConversionsService service

// Create принимает результат: POST /cpa/conversions.
//
// Ответ приезжает с 201, когда конверсия создана, и с 200 на идемпотентном
// повторе — **и то и другое успех**. Клиент, считающий успехом только 201,
// сломался бы на первом же повторе после сетевого сбоя, то есть ровно тогда,
// когда идемпотентность и нужна.
//
// ⚠⚠ Операция идемпотентна по тройке «сеть + external_id + goal», и ПОЭТОМУ её
// POST объявлен повторяемым — в отличие от Client.Do, который не повторяет
// запись никогда. Повтор уже исполненного запроса — тот же статус либо статус,
// до которого конверсия уже дошла (approved, досланный после холда), — вернёт
// ту же конверсию, а с другой суммой — 409 IDEMPOTENT_MISMATCH, а не второй
// платёж.
//
// ⚠ Другой статус — не повтор, а переход: подтверждение ожидавшего заказа
// (pending → approved) законно несёт итоговую сумму, и дверь её принимает.
// Недопустимый переход — 409 ILLEGAL_TRANSITION.
//
// Форма запроса проверяется ДО сети: обязательные поля, длины, потолок суммы,
// зарезервированный префикс. Отказ несёт ErrValidation и называет поле.
func (s *ConversionsService) Create(
	ctx context.Context,
	params CreateConversionParams,
) (*ConversionDetail, *Response, error) {
	if err := params.Validate(); err != nil {
		return nil, nil, err
	}

	return do[ConversionDetail](ctx, (*service)(s), request{
		Method:          http.MethodPost,
		Path:            conversionsPath,
		Body:            params,
		Subject:         "конверсия",
		IdempotentWrite: true,
	})
}

// List читает страницу конверсий: GET /cpa/conversions.
//
// ⚠ Строки приезжают в проекции списка (Conversion), без тройки ставок: маржа и
// выручка есть только в карточке. Это не экономия ответа, а граница проекции —
// список отдают и партнёрской стороне.
//
// ⚠ Неизвестный партнёр в фильтре — 404 PARTNER_NOT_FOUND, а не пустая страница:
// опечатка в идентификаторе и «конверсий не было» — разные факты, и второй ответ
// на первый вопрос прочитали бы как отсутствие данных.
func (s *ConversionsService) List(ctx context.Context, params ListConversionsParams) ([]Conversion, *Response, error) {
	if err := params.Validate(); err != nil {
		return nil, nil, err
	}

	return doList[Conversion](ctx, (*service)(s), request{
		Method:  http.MethodGet,
		Path:    conversionsPath,
		Query:   params.Query(),
		Subject: "страница конверсий",
	})
}

// Get читает карточку конверсии: GET /cpa/conversions/{external_id}.
//
// ⚠⚠ Конверсия адресуется ТРОЙКОЙ «сеть + external_id + цель». Goal здесь
// трёхсоставна ровно как при создании: nil — цель по умолчанию, указатель на
// пустую строку — конверсия с ПУСТОЙ целью. Свести их к одной строке нельзя:
// пустая цель — законное значение ключа.
//
// В карточке есть тройка ставок (payout + margin == revenue) и нога рекрутёра —
// то, чего нет в списке.
func (s *ConversionsService) Get(ctx context.Context, params GetConversionParams) (*ConversionDetail, *Response, error) {
	if err := params.Validate(); err != nil {
		return nil, nil, err
	}

	return do[ConversionDetail](ctx, (*service)(s), request{
		Method:  http.MethodGet,
		Path:    conversionsPath + "/" + pathSegment(params.ExternalID),
		Query:   params.Query(),
		Subject: "конверсия",
	})
}

// Cancel отклоняет конверсию или оформляет возврат:
// POST /cpa/conversions/{external_id}/cancel.
//
// ⚠⚠ Отменённая конверсия ТЕРМИНАЛЬНА: новое подтверждение оформляется НОВЫМ
// external_id, а не переводом этой обратно. Рассчитывать на «отменил и подтвердил
// заново» нельзя — второй раз дверь ответит 409 ILLEGAL_TRANSITION.
//
// Операция идемпотентна: повторная отмена ничего не меняет, поэтому POST объявлен
// повторяемым.
func (s *ConversionsService) Cancel(ctx context.Context, params CancelConversionParams) (*ConversionDetail, *Response, error) {
	if err := params.Validate(); err != nil {
		return nil, nil, err
	}

	return do[ConversionDetail](ctx, (*service)(s), request{
		Method:          http.MethodPost,
		Path:            conversionsPath + "/" + pathSegment(params.ExternalID) + "/cancel",
		Body:            params,
		Subject:         "отмена конверсии",
		IdempotentWrite: true,
	})
}
