package cpatypes

import (
	"strconv"
	"time"
)

// ClickStatus — что стало с переходом.
type ClickStatus string

const (
	// ClickOpen — переход принят и отправлен рекламодателю.
	ClickOpen ClickStatus = "open"
	// ClickTrafficback — переход принят и записан, но к рекламодателю НЕ
	// отправлен: исчерпан кап либо не подошло гео.
	//
	// ⚠ Это не ошибка запроса, поэтому код ответа успешный. Клиент, считающий
	// успехом только ClickOpen, принял бы штатный отказ за поломку.
	ClickTrafficback ClickStatus = "trafficback"
)

// Click — ответ POST /cpa/clicks.
//
// ⚠⚠ ClickID и RedirectURL объявлены в контракте обязательными И nullable
// одновременно, поэтому тега `cpa:"required"` у них НЕТ: тег означает «поле есть
// и не null», а у trafficback оба законно приходят nullʼом. Пометь их — и клиент
// отбивал бы штатный ответ двери как нарушение контракта.
type Click struct {
	// ClickID — null при ClickTrafficback: отбитый переход до рекламодателя не
	// дошёл и конверсию породить не может, так что выдать по нему идентификатор
	// значило бы пообещать невозможное.
	ClickID *ClickID    `json:"click_id"`
	Status  ClickStatus `json:"status" cpa:"required"`
	// Reason — почему переход отбит (cap, geo). null у обычного.
	Reason *string `json:"reason"`
	// RedirectURL — куда отправить посетителя. null означает «вести некуда»: кап
	// исчерпан, а запасного адреса рекламодатель не объявил. Показывайте свою
	// страницу.
	RedirectURL *string `json:"redirect_url"`
	// ExpiresAt — конец окна атрибуции этого перехода.
	ExpiresAt         *time.Time         `json:"expires_at"`
	CreatedAt         time.Time          `json:"created_at" cpa:"required"`
	OfferID           string             `json:"offer_id"`
	PartnerExternalID *PartnerExternalID `json:"partner_external_id"`
}

// CreateClickParams — вход POST /cpa/clicks.
//
// ⚠ occurred_at здесь нет и не будет: клик — это ПЕРЕХОД, и его время есть время
// вызова. Досылать переходы пачкой нельзя по построению (в ответ приезжает адрес,
// на который человека уже отправили), а принять чужое время значило бы разрешить
// задним числом переписать расход капа.
type CreateClickParams struct {
	PartnerExternalID PartnerExternalID `json:"partner_external_id"`
	OfferID           string            `json:"offer_id"`
	// ClientClickID — необязательный ключ идемпотентности отправителя. Прислали —
	// повтор возвращает ТОТ ЖЕ клик (200 вместо 201); не прислали — каждый вызов
	// новый переход.
	//
	// ⚠⚠ Ключ принадлежит ПАРЕ «партнёр + оффер»: тот же ключ с другой парой —
	// 409 IDEMPOTENT_MISMATCH, а не сохранённый переход. Иначе опечатка в
	// счётчике вернула бы адрес чужого рекламодателя, а конверсию платформа
	// приписала бы не тому партнёру.
	ClientClickID string `json:"client_click_id,omitempty"`
	SubID         string `json:"sub_id,omitempty"`
	SubID2        string `json:"sub_id_2,omitempty"`
	SubID3        string `json:"sub_id_3,omitempty"`
	SubID4        string `json:"sub_id_4,omitempty"`
	SubID5        string `json:"sub_id_5,omitempty"`
	// Country — ISO 3166-1 alpha-2.
	//
	// ⚠⚠ Адрес посетителя и строка его браузера дверью НЕ принимаются: раздел 3
	// контракта запрещает персональные данные «ни партнёра, ни его клиента», а
	// IP — именно они. Страна и есть та величина, ради которой адрес читают.
	Country string `json:"country,omitempty"`
}

// maxClickTextLen — предел sub_id и client_click_id у этой двери.
const maxClickTextLen = 200

// maxOfferIDLen — предел offer_id у двери клика.
const maxOfferIDLen = 120

// Validate проверяет форму до отправки.
func (p CreateClickParams) Validate() error {
	if err := p.PartnerExternalID.Validate(); err != nil {
		return err
	}
	switch {
	case p.OfferID == "":
		return invalid("offer_id", "обязательное поле")
	case len(p.OfferID) > maxOfferIDLen:
		return invalid("offer_id", "длиннее "+strconv.Itoa(maxOfferIDLen)+" символов")
	}
	if len(p.ClientClickID) > maxClickTextLen {
		return invalid("client_click_id", "длиннее "+strconv.Itoa(maxClickTextLen)+" символов")
	}
	for i, sub := range []string{p.SubID, p.SubID2, p.SubID3, p.SubID4, p.SubID5} {
		if len(sub) > maxClickTextLen {
			field := "sub_id"
			if i > 0 {
				field = "sub_id_" + strconv.Itoa(i+1)
			}
			return invalid(field, "длиннее "+strconv.Itoa(maxClickTextLen)+" символов")
		}
	}
	// ⚠ Два знака, а не «похоже на страну»: контракт требует alpha-2, и трёхбуквенный
	// код — самая частая подмена (RUS вместо RU). Дверь его отбила бы, но названное
	// поле полезнее кода 400 без объяснения.
	if p.Country != "" && len(p.Country) != 2 {
		return invalid("country", "ожидается код ISO 3166-1 alpha-2 из двух знаков, пришло "+strconv.Quote(p.Country))
	}
	return nil
}
