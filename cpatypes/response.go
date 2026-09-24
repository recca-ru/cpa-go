package cpatypes

import "net/http"

// Meta — блок meta конверта (раздел 4).
//
// Все поля страницы — указатели, потому что «не прислали» и «прислали ноль» у
// них разные: список из нуля строк и список, которого не считали, — разные
// ответы.
type Meta struct {
	// RequestID приходит и на успехе, и на отказе. Называйте его, сообщая о
	// проблеме: по нему находится вся цепочка на стороне Recca.
	RequestID string `json:"request_id"`
	Total     *int   `json:"total,omitempty"`
	Count     *int   `json:"count,omitempty"`
	Offset    *int   `json:"offset,omitempty"`
	Limit     *int   `json:"limit,omitempty"`
	HasMore   *bool  `json:"has_more,omitempty"`
	// Truncated со значением true означает, что выдача НЕПОЛНА: дверь упёрлась
	// в потолок просмотра, и Total — сколько успели посчитать, а не сколько
	// есть.
	//
	// ⚠⚠ Поле приходит всегда, включая false: «мы посмотрели всё» — это
	// утверждение, а не отсутствие оговорки. Поэтому указатель: иначе
	// неполная выдача была бы неотличима от полной.
	Truncated *bool `json:"truncated,omitempty"`
}

// Response — то, что клиент знает об ответе помимо самих данных.
type Response struct {
	// StatusCode — HTTP-статус ответа.
	StatusCode int
	// RequestID — итоговый идентификатор запроса: заголовок X-Request-Id, если
	// он есть, иначе meta.request_id.
	//
	// ⚠ Приоритет у заголовка не случаен: два отказа формируются до того, как
	// запрос дойдёт до метода, и тела с meta у них нет вовсе — «ключ не
	// предъявлен» (401) и «исчерпан бюджет» (429).
	RequestID string
	// Meta — блок meta так, как его прислала дверь. Пуст, если тела не было.
	Meta Meta
	// Header — заголовки ответа: Retry-After, X-RateLimit-Limit,
	// X-RateLimit-Remaining и прочее, что нужно для собственного учёта.
	Header http.Header
}
