package cpatypes

import (
	"encoding/json"
	"net/url"
	"time"
)

// PartnerRole — роль партнёра в сети.
//
// ⚠ «Инфлюенсер» — не отдельная роль, а вебмастер: поверхности совпадают до
// строчки, отличается источник трафика, и он остаётся у интегратора.
type PartnerRole string

const (
	PartnerWebmaster PartnerRole = "webmaster"
	PartnerAgent     PartnerRole = "agent"
	// PartnerAdvertiser встречается только в ЧТЕНИИ: рекламодателя машинная
	// дверь не заводит — у него карточка-витрина, офферы и договор, то есть
	// решения оператора в кабинете.
	PartnerAdvertiser PartnerRole = "advertiser"
)

// PartnerState — «человек у нас ушёл». Пишет ИНТЕГРАТОР через POST /cpa/partners.
type PartnerState string

const (
	PartnerStateActive   PartnerState = "active"
	PartnerStateInactive PartnerState = "inactive"
)

// PartnerStatus — «наказан за фрод». Пишет ОПЕРАТОР в кабинете.
//
// ⚠⚠ Два поля, а не одно, и это решение контракта: upsert из API молча снимал бы
// санкцию оператора. Право работать = активны оба.
type PartnerStatus string

const (
	PartnerStatusActive PartnerStatus = "active"
	// PartnerStatusPending — роль выдана, заявка ещё в очереди оператора. Это
	// третье состояние, а не оттенок первых двух: означает «ещё не работает», но
	// санкции за ним нет, и сообщать о ней было бы неправдой.
	PartnerStatusPending   PartnerStatus = "pending"
	PartnerStatusSuspended PartnerStatus = "suspended"
)

// Partner — карточка партнёра.
//
// ⚠ Ни имени, ни почты, ни телефона здесь нет и не будет (раздел 3 контракта).
type Partner struct {
	ExternalID PartnerExternalID `json:"external_id" cpa:"required"`
	// TenantReccaID — наш непрозрачный идентификатор этого человека В ЭТОЙ СЕТИ.
	//
	// ⚠ Суррогат разный у разных сетей по построению: связать по нему одного
	// человека между сетями нельзя, и это не недосмотр.
	TenantReccaID string        `json:"tenant_recca_id" cpa:"required"`
	Role          PartnerRole   `json:"role" cpa:"required"`
	PartnerState  PartnerState  `json:"partner_state" cpa:"required"`
	Status        PartnerStatus `json:"status" cpa:"required"`
	// RecruiterExternalID — кто привёл этого партнёра; null, если никто.
	RecruiterExternalID *PartnerExternalID `json:"recruiter_external_id"`
	CreatedAt           time.Time          `json:"created_at" cpa:"required"`
}

// PartnerBalance — ответ GET /cpa/partners/{external_id}/balance.
type PartnerBalance struct {
	// AvailableRUB — ровно то, что можно запросить к выплате сейчас.
	AvailableRUB RUB `json:"available_rub" cpa:"required"`
	// OnHoldRUB станет доступным по истечении холда конверсий; отдельного
	// запроса не нужно.
	OnHoldRUB    RUB `json:"on_hold_rub" cpa:"required"`
	PaidTotalRUB RUB `json:"paid_total_rub" cpa:"required"`
	// MinPayoutRUB — порог сети. Читать отсюда, а не из своей константы.
	MinPayoutRUB RUB  `json:"min_payout_rub" cpa:"required"`
	CanRequest   bool `json:"can_request" cpa:"required"`
}

// UpsertPartnerParams — вход POST /cpa/partners: регистрация партнёра и его
// деактивация.
//
// ⚠ Повтор на уже заведённого партнёра меняет ТОЛЬКО поля интегратора —
// partner_state и рекрутёра; санкцию оператора он не трогает.
//
// ⚠⚠ Role и AgentPercent на уже заведённом партнёре принадлежат ОПЕРАТОРУ: другое
// значение на повторе — 400 INVALID_INPUT (поле role либо agent_percent), а не
// тихое применение. Синхронизация, шлющая роль со своей стороны, начнёт падать на
// партнёре, которого оператор перевёл в агенты. Подробно — PartnersService.Upsert.
//
// ⚠⚠ Имени, почты, телефона и реквизитов здесь нет и не появится: распознанное
// персональное значение дверь отбивает 400 PII_NOT_ACCEPTED.
type UpsertPartnerParams struct {
	// ExternalID — ключ идемпотентности и единственный способ адресовать
	// человека.
	ExternalID PartnerExternalID `json:"external_id"`
	// Role принимает только PartnerWebmaster и PartnerAgent.
	Role PartnerRole `json:"role"`
	// PartnerState — пустая строка означает «ключа в теле нет», то есть
	// «не меняем».
	PartnerState PartnerState `json:"partner_state,omitempty"`
	// RecruiterExternalID — прикрепить или сменить рекрутёра. Пустая строка
	// означает «не меняем»; открепляет DetachRecruiter.
	RecruiterExternalID PartnerExternalID `json:"recruiter_external_id,omitempty"`
	// DetachRecruiter — открепить рекрутёра: в теле уходит явный
	// `"recruiter_external_id": null`. Действует ВПЕРЁД: уже начисленные ноги
	// агента не пересчитываются, новые конверсии партнёра приходят без неё.
	// Партнёр без рекрутёра — успех без изменений, повтор безопасен.
	//
	// ⚠ Отдельное поле, а не указатель на RecruiterExternalID: у двери три
	// состояния (ключа нет · строка · null), и «не меняю» с «открепить» не должны
	// различаться одним nil, который в Go получают по забывчивости. Вместе с
	// непустым RecruiterExternalID — отказ до сети.
	DetachRecruiter bool `json:"-"`
	// AgentPercent — ставка агентской ноги, только при Role == PartnerAgent. Для
	// вебмастера дверь её игнорирует. Задаётся при ЗАВЕДЕНИИ: у действующего
	// агента другое значение на повторе — 400 INVALID_INPUT, ставку меняет
	// оператор.
	//
	// Указатель, потому что 0 — законная ставка, а «не задавали» от неё
	// отличается.
	AgentPercent *json.Number `json:"agent_percent,omitempty"`
}

// MarshalJSON пишет явный null рекрутёра при DetachRecruiter. Без флага тело
// то же, что дали бы теги.
func (p UpsertPartnerParams) MarshalJSON() ([]byte, error) {
	type plain UpsertPartnerParams
	if !p.DetachRecruiter {
		return json.Marshal(plain(p))
	}
	// Поле внешней структуры мельче встроенного и побеждает его ключ: omitempty
	// встроенного здесь не действует, в тело уходит null.
	return json.Marshal(struct {
		plain
		Recruiter *PartnerExternalID `json:"recruiter_external_id"`
	}{plain: plain(p)})
}

// Validate проверяет форму до отправки.
func (p UpsertPartnerParams) Validate() error {
	if err := p.ExternalID.Validate(); err != nil {
		return err
	}
	switch p.Role {
	case PartnerWebmaster, PartnerAgent:
	case "":
		return invalid("role", "обязательное поле: webmaster или agent")
	case PartnerAdvertiser:
		// ⚠ Отдельная ветка с отдельным текстом: роль существует в чтении, и
		// общий ответ «не та роль» отправил бы интегратора искать опечатку там,
		// где её нет.
		return invalid("role", "рекламодателя машинная дверь не заводит — его карточку создаёт оператор в кабинете Recca")
	default:
		return invalid("role", "неизвестная роль "+string(p.Role)+": допустимы webmaster и agent")
	}
	switch p.PartnerState {
	case "", PartnerStateActive, PartnerStateInactive:
	default:
		return invalid("partner_state", "неизвестное значение "+string(p.PartnerState)+": допустимы active и inactive")
	}
	if p.RecruiterExternalID != "" {
		if p.DetachRecruiter {
			return invalid("recruiter_external_id", "задано и имя рекрутёра, и DetachRecruiter: прикрепить и открепить разом нельзя")
		}
		if err := p.RecruiterExternalID.Validate(); err != nil {
			return invalid("recruiter_external_id", "форма не проходит: "+err.Error())
		}
	}
	if p.AgentPercent != nil {
		if !validNumber(*p.AgentPercent) {
			return invalid("agent_percent", "не является числом JSON")
		}
		value, _ := p.AgentPercent.Float64()
		if value < 0 || value > 100 {
			return invalid("agent_percent", "ставка вне диапазона 0…100")
		}
	}
	return nil
}

// ListPartnersParams — фильтры GET /cpa/partners.
type ListPartnersParams struct {
	// Role — чьи карточки вернуть.
	Role PartnerRole
	// Status — фильтр по санкции оператора.
	Status PartnerStatus
	// RecruiterExternalID — «мои блогеры» в кабинете агента.
	RecruiterExternalID PartnerExternalID
	PageParams
}

// Query собирает строку запроса. Форма живёт рядом с типом — одной копией, как и
// у тела запроса.
func (p ListPartnersParams) Query() url.Values {
	q := url.Values{}
	setIfNotEmpty(q, "role", string(p.Role))
	setIfNotEmpty(q, "status", string(p.Status))
	setIfNotEmpty(q, "recruiter_external_id", string(p.RecruiterExternalID))
	p.PageParams.addTo(q)
	return q
}

// Validate проверяет то, в чём клиент прав наверняка.
func (p ListPartnersParams) Validate() error {
	if err := p.PageParams.Validate(); err != nil {
		return err
	}
	if p.RecruiterExternalID != "" {
		if err := p.RecruiterExternalID.Validate(); err != nil {
			return invalid("recruiter_external_id", "форма не проходит: "+err.Error())
		}
	}
	return nil
}
