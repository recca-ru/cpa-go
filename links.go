package cpa

import (
	"context"
	"net/http"
)

const linksPath = "/cpa/links"

// LinksService — выдача стабильных ссылок партнёра на оффер.
type LinksService service

// Create выдаёт ссылку: POST /cpa/links.
//
// Успех — и 201 (ссылка создана), и 200 (та же пара «партнёр + оффер» уже имеет
// ссылку).
//
// ⚠⚠ TargetURL в ответе — адрес НА СТОРОНЕ RECCA (/go/{code}), а не посадочная
// оффера. Подмена стоила бы денег молча: уведи клиента прямо на посадочную —
// перехода не будет засчитано, и не будет ни клика, ни атрибуции, ни выплаты
// блогеру, и ни одной ошибки в ответ.
//
// ⚠ Партнёр, которого интегратор отключил, ссылку не получает: 409
// PARTNER_NOT_ACTIVE. Право работать = активны И участие, и санкция оператора.
//
// Операция идемпотентна по паре «партнёр + оффер», ключ в теле, поэтому POST
// объявлен повторяемым.
func (s *LinksService) Create(ctx context.Context, params CreateLinkParams) (*Link, *Response, error) {
	if err := params.Validate(); err != nil {
		return nil, nil, err
	}

	return do[Link](ctx, (*service)(s), request{
		Method:          http.MethodPost,
		Path:            linksPath,
		Body:            params,
		Subject:         "ссылка",
		IdempotentWrite: true,
	})
}
