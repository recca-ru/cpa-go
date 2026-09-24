package cpa

import (
	"context"
	"net/http"
)

const (
	selfPath        = "/cpa/self"
	selfWebhookPath = "/cpa/self/webhook"
)

// SelfService — сведения о самой сети: проба живости и настройки, которые нельзя
// знать заранее.
type SelfService service

// Get читает GET /cpa/self: версию контракта, скоупы ключа, базу и срок ноги
// рекрутёра, порог выплаты и ФАКТИЧЕСКИЙ бюджет запросов.
//
// ⚠ Эта проба не входит в счёт ограничителя — иначе мониторинг тратил бы бюджет
// интегратора. Поэтому она же годится как проверка живости при запуске.
//
// ⚠⚠ RateLimitPerMin брать ОТСЮДА, а не из таблицы величин контракта: там
// гарантированный минимум и потолок, а здесь то самое число, по которому дверь
// принимает решение о 429.
func (s *SelfService) Get(ctx context.Context) (*SelfInfo, *Response, error) {
	return do[SelfInfo](ctx, (*service)(s), request{
		Method:  http.MethodGet,
		Path:    selfPath,
		Subject: "сведения о сети",
	})
}

// SetWebhook объявляет адрес для событий cpa.*: PUT /cpa/self/webhook.
//
// ⚠⚠ Секрет подписи в ответе показывается ОДИН раз — при ПЕРВОЙ установке адреса
// и при каждой ротации. Сохраните его сразу: повторно дверь его не выдаёт, и
// второго способа узнать его нет. Проверять подпись пришедшего события — пакет
// webhook.
//
// ⚠⚠ Метод НЕ повторяется при сбое никогда — ни с ротацией, ни без неё. Дверь
// записывает секрет на первой попытке; если ответ потерялся, повтор получил бы
// успех с Secret == nil (секрет уже есть и второй раз не показывается), и секрет
// был бы потерян МОЛЧА — ни ошибки, ни способа его узнать.
//
// Не получили ответа — повторите вызов сами с RotateSecret: true: ротация выдаст
// новый секрет, и он придёт в ответе.
//
// ⚠ RotateSecret — это ОТЗЫВ прежнего секрета, а не добавление второго: события,
// подписанные старым, после ротации проверку не пройдут.
func (s *SelfService) SetWebhook(ctx context.Context, params SetWebhookParams) (*WebhookConfig, *Response, error) {
	if err := params.Validate(); err != nil {
		return nil, nil, err
	}

	return do[WebhookConfig](ctx, (*service)(s), request{
		Method:  http.MethodPut,
		Path:    selfWebhookPath,
		Body:    params,
		Subject: "настройка вебхука",
		// IdempotentWrite не выставляется НИКОГДА. PUT выглядит идемпотентным, но
		// ответ этой двери — не только состояние: в нём единственный показ
		// секрета, и повтор отдал бы то же состояние уже без него. Замер двери:
		// `!hadSecret || rotate` в api/federation/v1/cpa/self/webhook/route.ts.
	})
}
