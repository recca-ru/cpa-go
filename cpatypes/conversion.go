package cpatypes

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ConversionStatus — состояние конверсии.
//
// ⚠ Отдельного reversed здесь нет намеренно: возврат приводит конверсию в
// declined, а различие «отклонили» ⇆ «вернули» живёт в событии вебхука. Два
// имени одного состояния разошлись бы.
type ConversionStatus string

const (
	// ConversionPending — о событии сообщили, но не подтвердили; денежной ноги ещё нет.
	ConversionPending ConversionStatus = "pending"
	// ConversionHold — подтверждено, выплата зарезервирована, идут часы холда.
	ConversionHold ConversionStatus = "hold"
	// ConversionApproved — холд истёк, выплата доступна.
	ConversionApproved ConversionStatus = "approved"
	// ConversionDeclined — отклонено или возвращено.
	ConversionDeclined ConversionStatus = "declined"
	// ConversionNotFound встречается только у конверсий, пришедших ПРЯМЫМ
	// постбэком рекламодателя с неизвестным платформе переходом.
	ConversionNotFound ConversionStatus = "not_found"
)

// Conversion — конверсия в проекции списка.
//
// Тег `cpa:"required"` означает «дверь присылает это поле всегда»: на таких
// полях клиент не соглашается на молчаливый ноль (см. RequireFields).
type Conversion struct {
	ExternalID string           `json:"external_id" cpa:"required"`
	Goal       string           `json:"goal" cpa:"required"`
	Status     ConversionStatus `json:"status" cpa:"required"`
	// PartnerExternalID — партнёр, которому засчитан результат. null означает,
	// что партнёр не определён, и это не то же самое, что пустая строка.
	PartnerExternalID *PartnerExternalID `json:"partner_external_id"`
	OfferID           string             `json:"offer_id"`
	AdvertiserID      string             `json:"advertiser_id"`
	// PayoutRUB объявлен обязательным по ЗАМЕРУ КОДА, а не по openapi: в
	// `required` схемы Conversion его нет, но проектор платформы ставит его
	// всегда и не nullʼом (`utils/cpa-api-projection.ts:497`,
	// `payout_rub: number`). Молчаливый ноль именно здесь — сумма выплаты, то
	// есть дословно класс LOOP-01.
	//
	// ⚠ Расхождение документа с кодом названо в журнале волны как остаток: если
	// дверь однажды законно пришлёт карточку без этого поля, тег снимать здесь.
	PayoutRUB RUB `json:"payout_rub" cpa:"required"`
	// SumRUB — сумма заказа так, как её прислали: число может быть дробным,
	// поэтому лексическая форма сохраняется (см. RUB).
	SumRUB    *json.Number `json:"sum_rub"`
	SubID     *string      `json:"sub_id"`
	CreatedAt time.Time    `json:"created_at" cpa:"required"`
	// OccurredAt — когда событие случилось у интегратора. null значит «не
	// сказали»; отчёты и окна считаются по occurred_at ?? created_at.
	OccurredAt *time.Time `json:"occurred_at"`
	HoldUntil  *time.Time `json:"hold_until"`
}

// AgentLegBase — от чего считалась нога рекрутёра.
type AgentLegBase string

const (
	AgentLegFromMargin AgentLegBase = "margin"
	AgentLegFromPayout AgentLegBase = "payout"
)

// AgentLeg — нога рекрутёра, приведшего партнёра.
//
// ⚠ Значения заморожены на момент конверсии: текущая настройка сети в
// GET /cpa/self может быть уже другой. Условия действуют вперёд, а карточка
// объясняет записанное.
type AgentLeg struct {
	ExternalID PartnerExternalID `json:"external_id"`
	AmountRUB  RUB               `json:"amount_rub"`
	Base       AgentLegBase      `json:"base"`
	// Percent — ставка, по которой ногу посчитали. null означает, что нога
	// записана до заморозки условий: значения не существует, и подставлять
	// текущую ставку было бы враньём.
	Percent *json.Number `json:"percent"`
	// HoldUntil — когда нога станет доступной рекрутёру к выводу.
	HoldUntil *time.Time `json:"hold_until"`
}

// ConversionDetail — карточка конверсии: тройка ставок и нога рекрутёра.
type ConversionDetail struct {
	Conversion
	// RevenueRUB, PayoutRUB и MarginRUB сходятся: payout + margin == revenue.
	// Это и есть доказательство приёмки по п. 3.4 договора, поэтому в типе
	// присутствуют все три, а не только выплата, и все три обязательны.
	RevenueRUB RUB       `json:"revenue_rub" cpa:"required"`
	MarginRUB  RUB       `json:"margin_rub" cpa:"required"`
	AgentLeg   *AgentLeg `json:"agent_leg"`
	// FraudFlags — пометки качества трафика; сами по себе не блокируют.
	FraudFlags []string `json:"fraud_flags"`
}

// CreateConversionParams — вход POST /cpa/conversions.
//
// ⚠⚠ Три состояния суммы: «не передали» (nil), «ноль» и «значение» — разные
// утверждения, и ПОВТОР (тот же статус) с другим из них дверь отбивает 409
// IDEMPOTENT_MISMATCH. Переход со сменой статуса сумму менять вправе:
// подтверждение ожидавшего заказа приходит с итоговой суммой.
// У SubID различать нечего — пустая строка и есть «не передали».
//
// ⚠ У Goal состояний меньше, чем кажется: дверь приводит отсутствие ключа и
// пустую строку к цели по умолчанию "1" (normalizeGoal в Recca), поэтому nil,
// Ptr("") и Ptr("1") адресуют ОДНУ конверсию. Различаются только цель по
// умолчанию и названная цель. SDK передаёт ключ как есть: присутствие ключа
// значимо на двери подписанного постбэка (канон подписи), а не здесь.
type CreateConversionParams struct {
	// ClickID — переход, по которому засчитывается результат.
	ClickID ClickID
	// ExternalID — номер заказа на стороне интегратора.
	//
	// ⚠ Пространство номеров общее для СЕТИ: у двух рекламодателей со сквозной
	// нумерацией они пересекутся.
	ExternalID string
	// Status — подтверждение либо ожидание/отмена; словарь на стороне двери.
	Status string
	// Goal — цель. nil и указатель на пустую строку — цель по умолчанию "1";
	// см. заголовок типа.
	Goal *string
	// SumRUB — сумма заказа целыми рублями. nil означает «сумма неизвестна»,
	// указатель на 0 — «известна и равна нулю».
	SumRUB *RUB
	// SumRUBExact — та же сумма лексической формой, когда она дробная
	// («12.5»). Взаимоисключима с SumRUB: две двери к одной сумме — это две
	// правды о деньгах.
	SumRUBExact *json.Number
	// SubID — метка источника трафика.
	SubID string
	// OccurredAt — когда событие случилось у интегратора. Не прислали — дверь
	// берёт момент приёма.
	OccurredAt *time.Time
}

// conversionWire — форма тела запроса. Порядок полей здесь и есть порядок в
// JSON, а присутствие ключа значимо, поэтому опущенные поля — указатели.
type conversionWire struct {
	ClickID    ClickID `json:"click_id"`
	ExternalID string  `json:"external_id"`
	Status     string  `json:"status"`
	Goal       *string `json:"goal,omitempty"`
	// SumRUB — сырое число: и целая, и дробная форма пишутся как есть, без
	// повторного разбора и форматирования.
	SumRUB     *json.RawMessage `json:"sum_rub,omitempty"`
	SubID      string           `json:"sub_id,omitempty"`
	OccurredAt *time.Time       `json:"occurred_at,omitempty"`
}

// MarshalJSON собирает тело запроса.
func (p CreateConversionParams) MarshalJSON() ([]byte, error) {
	wire := conversionWire{
		ClickID:    p.ClickID,
		ExternalID: p.ExternalID,
		Status:     p.Status,
		Goal:       p.Goal,
		SubID:      p.SubID,
		OccurredAt: p.OccurredAt,
	}

	switch {
	case p.SumRUBExact != nil:
		raw := json.RawMessage(p.SumRUBExact.String())
		wire.SumRUB = &raw
	case p.SumRUB != nil:
		raw := json.RawMessage(strconv.FormatInt(int64(*p.SumRUB), 10))
		wire.SumRUB = &raw
	}

	return json.Marshal(wire)
}

// Validate проверяет ФОРМУ запроса до отправки.
//
// ⚠ Здесь только то, что клиент может утверждать сам: обязательные поля,
// длины, алфавит, потолок суммы, зарезервированный префикс. Всё остальное —
// существует ли оффер, открыто ли окно атрибуции, свободен ли ключ
// идемпотентности — знает только дверь, и подменять её ответ догадкой значило
// бы обещать то, чего клиент не исполняет.
func (p CreateConversionParams) Validate() error {
	if p.SumRUB != nil && p.SumRUBExact != nil {
		return invalid("sum_rub", "заданы обе формы суммы, а сумма у операции одна")
	}
	if p.ClickID == "" {
		return invalid("click_id", "обязательное поле")
	}
	if err := validateExternalID(p.ExternalID); err != nil {
		return err
	}
	if IsReservedExternalID(p.ExternalID) {
		return invalid("external_id", "префикс "+ReservedExternalIDPrefix+
			" (в любом регистре и с пробелами впереди) принадлежит внутренним операциям платформы")
	}
	if p.Status == "" {
		return invalid("status", "обязательное поле")
	}
	if p.Goal != nil && len(*p.Goal) > MaxGoalLen {
		return invalid("goal", "длиннее "+strconv.Itoa(MaxGoalLen)+" символов")
	}
	if len(p.SubID) > MaxSubIDLen {
		return invalid("sub_id", "длиннее "+strconv.Itoa(MaxSubIDLen)+" символов")
	}
	if p.SumRUB != nil {
		if *p.SumRUB < 0 {
			return invalid("sum_rub", "отрицательная сумма")
		}
		if *p.SumRUB > MaxSumRUB {
			return invalid("sum_rub", "выше потолка контракта "+MaxSumRUB.String())
		}
	}
	if p.SumRUBExact != nil {
		if !validNumber(*p.SumRUBExact) {
			return invalid("sum_rub", "лексическая форма "+strconv.Quote(p.SumRUBExact.String())+" не является числом JSON")
		}
		value, _ := p.SumRUBExact.Float64()
		if value < 0 {
			return invalid("sum_rub", "отрицательная сумма")
		}
		if value > float64(MaxSumRUB) {
			return invalid("sum_rub", "выше потолка контракта "+MaxSumRUB.String())
		}
	}
	return nil
}

// ListConversionsParams — фильтры GET /cpa/conversions.
type ListConversionsParams struct {
	// PartnerExternalID — чьи конверсии.
	//
	// ⚠ Неизвестный партнёр — 404 PARTNER_NOT_FOUND, а не пустой список: опечатка
	// в идентификаторе и «конверсий не было» — разные факты.
	PartnerExternalID PartnerExternalID
	AdvertiserID      string
	OfferID           string
	Status            ConversionStatus
	// From и To — МОМЕНТЫ, а не календарные даты: у этой двери контракт объявляет
	// `date-time`. У отчёта наоборот — `date`, и подставить туда момент значило бы
	// послать форму, которой дверь не ждёт.
	From *time.Time
	To   *time.Time
	PageParams
}

// Query собирает строку запроса.
func (p ListConversionsParams) Query() url.Values {
	q := url.Values{}
	setIfNotEmpty(q, "partner_external_id", string(p.PartnerExternalID))
	setIfNotEmpty(q, "advertiser_id", p.AdvertiserID)
	setIfNotEmpty(q, "offer_id", p.OfferID)
	setIfNotEmpty(q, "status", string(p.Status))
	if p.From != nil {
		q.Set("from", p.From.Format(time.RFC3339))
	}
	if p.To != nil {
		q.Set("to", p.To.Format(time.RFC3339))
	}
	p.PageParams.addTo(q)
	return q
}

// Validate проверяет то, в чём клиент прав наверняка.
func (p ListConversionsParams) Validate() error {
	if err := p.PageParams.Validate(); err != nil {
		return err
	}
	if p.PartnerExternalID != "" {
		if err := p.PartnerExternalID.Validate(); err != nil {
			return err
		}
	}
	if p.From != nil && p.To != nil && p.To.Before(*p.From) {
		return invalid("to", "конец окна раньше начала")
	}
	return nil
}

// GetConversionParams — адресация карточки конверсии.
//
// ⚠⚠ Конверсия адресуется ТРОЙКОЙ «сеть + external_id + цель», и цель здесь
// трёхсоставна ровно как при создании: nil означает «цель по умолчанию»,
// указатель на пустую строку — конверсию с ПУСТОЙ целью. Свести их к одной
// строке нельзя: пустая цель — законное значение ключа, и «не указали» тогда
// стало бы неотличимо от неё.
type GetConversionParams struct {
	ExternalID string
	Goal       *string
}

// Query собирает строку запроса.
func (p GetConversionParams) Query() url.Values {
	q := url.Values{}
	if p.Goal != nil {
		q.Set("goal", *p.Goal)
	}
	return q
}

// Validate проверяет форму до отправки.
func (p GetConversionParams) Validate() error {
	if err := validateExternalID(p.ExternalID); err != nil {
		return err
	}
	if p.Goal != nil && len(*p.Goal) > MaxGoalLen {
		return invalid("goal", "длиннее "+strconv.Itoa(MaxGoalLen)+" символов")
	}
	return nil
}

// maxCancelReasonLen — предел причины отмены.
const maxCancelReasonLen = 500

// CancelConversionParams — тело POST /cpa/conversions/{external_id}/cancel.
//
// ⚠ Отмена идемпотентна: повторная ничего не меняет. Отменённая конверсия
// терминальна — новое подтверждение оформляется НОВЫМ external_id, а не
// переводом этой обратно.
type CancelConversionParams struct {
	// ExternalID — какую конверсию отменяем. В путь, а не в тело.
	ExternalID string `json:"-"`
	// Goal — та же тройка адресации, что у чтения: nil — цель по умолчанию.
	Goal *string `json:"goal,omitempty"`
	// Reason — для вашего же разбора: дверь его сохраняет и показывает оператору.
	Reason string `json:"reason,omitempty"`
}

// Validate проверяет форму до отправки.
func (p CancelConversionParams) Validate() error {
	if err := validateExternalID(p.ExternalID); err != nil {
		return err
	}
	if p.Goal != nil && len(*p.Goal) > MaxGoalLen {
		return invalid("goal", "длиннее "+strconv.Itoa(MaxGoalLen)+" символов")
	}
	if len(p.Reason) > maxCancelReasonLen {
		return invalid("reason", "длиннее "+strconv.Itoa(maxCancelReasonLen)+" символов")
	}
	return nil
}

// validateExternalID проверяет ФОРМУ номера заказа: он есть и укладывается в
// предел. Зарезервированный префикс здесь НЕ проверяется — см.
// IsReservedExternalID.
func validateExternalID(id string) error {
	switch {
	case id == "":
		return invalid("external_id", "обязательное поле")
	case len(id) > MaxExternalIDLen:
		return invalid("external_id", "длиннее "+strconv.Itoa(MaxExternalIDLen)+" символов")
	}
	return nil
}

// IsReservedExternalID отвечает, принадлежит ли номер заказа внутреннему
// пространству ключей платформы.
//
// ⚠⚠ Правило — дословно правило двери: `externalId.trim().toLowerCase()
// .startsWith("recca:")` (utils/conversion-external-id.ts в Recca). Сравнение без
// обрезки и без регистра пропускало бы `RECCA:x` и ` recca:x` до сети, и отказ
// приходил бы от двери вместо клиента — то есть проверка до сети делала бы вид,
// что держит границу, которую держит не она.
//
// ⚠ Применяется только при СОЗДАНИИ конверсии: двери чтения и отмены префикс не
// проверяют (замер 23.09), и отказ до сети там обещал бы то, чего дверь не
// утверждает.
func IsReservedExternalID(id string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(id)), ReservedExternalIDPrefix)
}
