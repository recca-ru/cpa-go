package cpatypes

import (
	"encoding/json"
	"net/url"
	"time"
)

// PayoutStatus — состояние заявки на выплату.
//
// ⚠ Отметку «выплачено» ставит ОПЕРАТОР в кабинете: деньги уходят с его счёта, и
// подтверждает это человек. Машинная дверь заявку создаёт, но не закрывает.
type PayoutStatus string

const (
	PayoutRequested PayoutStatus = "requested"
	PayoutApproved  PayoutStatus = "approved"
	PayoutPaid      PayoutStatus = "paid"
	PayoutRejected  PayoutStatus = "rejected"
)

// Payout — заявка на выплату.
type Payout struct {
	ID                string            `json:"id" cpa:"required"`
	PartnerExternalID PartnerExternalID `json:"partner_external_id" cpa:"required"`
	AmountRUB         RUB               `json:"amount_rub" cpa:"required"`
	Status            PayoutStatus      `json:"status" cpa:"required"`
	PeriodFrom        Date              `json:"period_from"`
	PeriodTo          Date              `json:"period_to"`
	CreatedAt         time.Time         `json:"created_at" cpa:"required"`
	// PaidAt — когда оператор подтвердил уход денег. null, пока не подтвердил.
	PaidAt *time.Time `json:"paid_at"`
}

// PayoutParams — вход POST /cpa/payouts.
//
// ⚠⚠ Поля закрыты, и собрать эту структуру можно ТОЛЬКО через NewPayoutParams.
// Довод денежный, а не стилистический: личность заявки — ПАРТНЁР И ПЕРИОД, и
// период входит в ключ идемпотентности. Структурный литерал без окна
// скомпилировался бы, уехал в дверь с пустым периодом — и повтор в другом месяце
// попал бы в ТУ ЖЕ заявку. Это дословно находка F-45 реестра Recca: «в ключе
// вывода не было времени вовсе — выведший 1000 ₽ в январе получил бы мартовским
// запросом январскую выплату».
//
// ⚠ Сумма в ключ НЕ входит — она СВЕРЯЕТСЯ: повтор с другой суммой за тот же
// период — 409 IDEMPOTENT_MISMATCH, а не вторая заявка и не молчаливый успех по
// прежней сумме. Войди сумма в ключ, другая сумма стала бы другим ключом, то есть
// второй заявкой за тот же период, и 409 был бы недостижим вовсе. (Решение
// владельца 22.09; шапка utils/cpa-api-payouts.ts в Recca.)
type PayoutParams struct {
	partner PartnerExternalID
	amount  RUB
	from    Date
	to      Date
}

// NewPayoutParams собирает заявку целиком либо не собирает вовсе.
func NewPayoutParams(partner PartnerExternalID, amount RUB, from, to Date) (PayoutParams, error) {
	p := PayoutParams{partner: partner, amount: amount, from: from, to: to}
	if err := p.Validate(); err != nil {
		return PayoutParams{}, err
	}
	return p, nil
}

// Partner — партнёр, которому предназначена выплата.
func (p PayoutParams) Partner() PartnerExternalID { return p.partner }

// AmountRUB — сумма заявки.
func (p PayoutParams) AmountRUB() RUB { return p.amount }

// Period — окно учёта заявки.
func (p PayoutParams) Period() (from, to Date) { return p.from, p.to }

// payoutWire — форма тела запроса.
type payoutWire struct {
	PartnerExternalID PartnerExternalID `json:"partner_external_id"`
	AmountRUB         RUB               `json:"amount_rub"`
	PeriodFrom        Date              `json:"period_from"`
	PeriodTo          Date              `json:"period_to"`
}

// MarshalJSON собирает тело запроса.
func (p PayoutParams) MarshalJSON() ([]byte, error) {
	return json.Marshal(payoutWire{
		PartnerExternalID: p.partner,
		AmountRUB:         p.amount,
		PeriodFrom:        p.from,
		PeriodTo:          p.to,
	})
}

// Validate проверяет форму до отправки.
//
// ⚠ Вызывается и конструктором, и методом. Второй раз — не перестраховка: нулевое
// значение структуры (`var p PayoutParams`) конструктор не проходило, а в метод
// попасть может, и тогда единственная проверка стояла бы не на пути.
func (p PayoutParams) Validate() error {
	if err := p.partner.Validate(); err != nil {
		return err
	}
	switch {
	case p.amount <= 0:
		return invalid("amount_rub", "сумма заявки обязана быть положительной")
	// ⚠ Потолка суммы здесь нет намеренно. MaxSumRUB — потолок КОНВЕРСИИ; у
	// двери выплат он не объявлен (`amount_rub: z.number().int().min(1)`), и
	// отказ до сети тут отказывал бы в том, что дверь принимает.
	case p.from.IsZero():
		return invalid("period_from", "начало окна обязательно: окно входит в ключ идемпотентности выплаты")
	case p.to.IsZero():
		return invalid("period_to", "конец окна обязателен: окно входит в ключ идемпотентности выплаты")
	case p.to.Before(p.from.Time):
		return invalid("period_to", "конец окна раньше начала: "+p.from.String()+" … "+p.to.String())
	}
	return nil
}

// ListPayoutsParams — фильтры GET /cpa/payouts.
type ListPayoutsParams struct {
	PartnerExternalID PartnerExternalID
	Status            PayoutStatus
	PageParams
}

// Query собирает строку запроса.
func (p ListPayoutsParams) Query() url.Values {
	q := url.Values{}
	setIfNotEmpty(q, "partner_external_id", string(p.PartnerExternalID))
	setIfNotEmpty(q, "status", string(p.Status))
	p.PageParams.addTo(q)
	return q
}

// Validate проверяет то, в чём клиент прав наверняка.
func (p ListPayoutsParams) Validate() error {
	if err := p.PageParams.Validate(); err != nil {
		return err
	}
	if p.PartnerExternalID != "" {
		if err := p.PartnerExternalID.Validate(); err != nil {
			return err
		}
	}
	return nil
}
