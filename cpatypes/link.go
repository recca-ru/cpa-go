package cpatypes

import (
	"strconv"
	"time"
)

// Link — стабильная ссылка партнёра на оффер.
type Link struct {
	Code string `json:"code" cpa:"required"`
	// TargetURL — адрес перехода НА СТОРОНЕ RECCA (/go/{code}).
	//
	// ⚠⚠ Это НЕ посадочная оффера, и подмена стоила бы денег: уведи клиента
	// прямо на посадочную — переход не будет засчитан, и не будет ни клика, ни
	// атрибуции, ни выплаты блогеру, и ни одной ошибки в ответ.
	TargetURL         string            `json:"target_url" cpa:"required"`
	PartnerExternalID PartnerExternalID `json:"partner_external_id" cpa:"required"`
	OfferID           string            `json:"offer_id" cpa:"required"`
	CreatedAt         time.Time         `json:"created_at" cpa:"required"`
}

// maxLinkPartnerIDLen — предел partner_external_id у двери ссылки.
//
// ⚠ Двести, а не шестьдесят четыре: у этой двери openapi объявляет свой предел,
// шире общего. Алфавит при этом проверяется общей проверкой — она строже, и
// послать через ссылку то, что не проходит в партнёры, незачем.
const maxLinkPartnerIDLen = 200

// CreateLinkParams — вход POST /cpa/links.
type CreateLinkParams struct {
	PartnerExternalID PartnerExternalID `json:"partner_external_id"`
	OfferID           string            `json:"offer_id"`
}

// Validate проверяет форму до отправки.
func (p CreateLinkParams) Validate() error {
	if err := p.PartnerExternalID.Validate(); err != nil {
		return err
	}
	if len(p.PartnerExternalID) > maxLinkPartnerIDLen {
		return invalid("partner_external_id", "длиннее "+strconv.Itoa(maxLinkPartnerIDLen)+" символов")
	}
	if p.OfferID == "" {
		return invalid("offer_id", "обязательное поле")
	}
	return nil
}
