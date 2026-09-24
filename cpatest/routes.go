package cpatest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"go.recca.ru/cpa"
)

// apiPrefix — версионированный префикс, который подставляет транспорт клиента.
const apiPrefix = "/federation/v1"

// handle записывает обращение, отдаёт заготовленный отказ либо маршрутизирует.
func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	path := strings.TrimPrefix(r.URL.Path, apiPrefix)

	s.mu.Lock()
	s.requests = append(s.requests, Request{
		Method: r.Method, Path: path, Query: r.URL.Query(), Body: body,
	})
	requestID := "req_cpatest_" + strconv.Itoa(len(s.requests))
	pending := s.pending
	// ⚠ Одноразовая заготовка снимается ЗДЕСЬ, до ответа: иначе повторный вызов
	// получил бы тот же отказ, и «ровно один раз» превратилось бы в «до отмены».
	// Удержанная (HoldError) не снимается — её снимает только ClearError.
	if !s.held {
		s.pending = nil
	}
	s.mu.Unlock()

	if pending != nil {
		s.writeError(w, requestID, *pending, "cpatest: отказ заготовлен PrepareError")
		return
	}

	s.route(w, r, path, body, requestID)
}

// route — все восемнадцать путей контракта.
//
// ⚠ Разбор руками, а не http.ServeMux: стенду нужен ПУТЬ ОТ ВЕРСИИ в записи
// обращений и однозначный ответ на незнакомый путь. Незнакомый путь обязан быть
// громким — молчаливый 404 читался бы как «дверь есть, данных нет».
func (s *Server) route(w http.ResponseWriter, r *http.Request, path string, body []byte, requestID string) {
	segments := strings.Split(strings.Trim(path, "/"), "/")

	switch {
	case path == "/cpa/self" && r.Method == http.MethodGet:
		self := fixture("self.json")
		if s.environment != "" {
			self = patch(self, map[string]any{"environment": s.environment})
		}
		s.writeData(w, requestID, self, http.StatusOK, nil)

	case path == "/cpa/self/webhook" && r.Method == http.MethodPut:
		s.writeData(w, requestID, s.webhookOf(body), http.StatusOK, nil)

	case path == "/cpa/partners" && r.Method == http.MethodGet:
		s.writeList(w, requestID, []json.RawMessage{fixture("partner.json")})

	case path == "/cpa/partners" && r.Method == http.MethodPost:
		s.writeData(w, requestID, s.partnerOf(body), http.StatusCreated, nil)

	// /cpa/partners/{external_id}[/balance]
	case len(segments) == 3 && segments[0] == "cpa" && segments[1] == "partners" && r.Method == http.MethodGet:
		s.writeData(w, requestID, fixture("partner.json"), http.StatusOK, nil)

	case len(segments) == 4 && segments[1] == "partners" && segments[3] == "balance" && r.Method == http.MethodGet:
		s.writeData(w, requestID, s.balance(), http.StatusOK, nil)

	case path == "/cpa/offers" && r.Method == http.MethodGet:
		s.writeList(w, requestID, []json.RawMessage{s.offerFor(r)})

	case len(segments) == 3 && segments[1] == "offers" && r.Method == http.MethodGet:
		s.writeData(w, requestID, s.offerFor(r), http.StatusOK, nil)

	case len(segments) == 4 && segments[1] == "offers" && segments[3] == "creatives" && r.Method == http.MethodGet:
		var creatives []json.RawMessage
		if err := json.Unmarshal(fixture("creatives.json"), &creatives); err != nil {
			s.writeError(w, requestID, cpa.CodeInternalError, "cpatest: фикстура материалов не разобрана")
			return
		}
		s.writeList(w, requestID, creatives)

	case path == "/cpa/links" && r.Method == http.MethodPost:
		s.writeData(w, requestID, fixture("link.json"), http.StatusCreated, nil)

	case path == "/cpa/clicks" && r.Method == http.MethodPost:
		s.writeData(w, requestID, fixture("click.json"), http.StatusCreated, nil)

	case path == "/cpa/conversions" && r.Method == http.MethodGet:
		s.writeList(w, requestID, []json.RawMessage{stripTriple(fixture("conversion.json"))})

	case path == "/cpa/conversions" && r.Method == http.MethodPost:
		s.acceptConversion(w, body, requestID)

	case len(segments) == 3 && segments[1] == "conversions" && r.Method == http.MethodGet:
		s.writeData(w, requestID, s.applyTamper(fixture("conversion.json")), http.StatusOK, nil)

	case len(segments) == 4 && segments[1] == "conversions" && segments[3] == "cancel" && r.Method == http.MethodPost:
		s.writeData(w, requestID, withStatus(fixture("conversion.json"), "declined"), http.StatusOK, nil)

	case path == "/cpa/payouts" && r.Method == http.MethodGet:
		s.writeList(w, requestID, []json.RawMessage{fixture("payout.json")})

	case path == "/cpa/payouts" && r.Method == http.MethodPost:
		s.acceptPayout(w, body, requestID)

	case path == "/cpa/statistics" && r.Method == http.MethodGet:
		var rows []json.RawMessage
		if err := json.Unmarshal(fixture("statistics.json"), &rows); err != nil {
			s.writeError(w, requestID, cpa.CodeInternalError, "cpatest: фикстура отчёта не разобрана")
			return
		}
		s.writeList(w, requestID, rows)

	default:
		// ⚠⚠ Громко и с путём в сообщении: незнакомый путь означает, что либо SDK
		// стучится не туда, либо стенд отстал от контракта, — и оба случая надо
		// увидеть сразу, а не через час разбора пустого списка.
		s.writeError(w, requestID, cpa.CodeInternalError,
			fmt.Sprintf("cpatest: путь %s %s стендом не объявлен", r.Method, path))
	}
}

// ── формирование ответа ──────────────────────────────────────────────────────

func (s *Server) writeData(w http.ResponseWriter, requestID string, data json.RawMessage, status int, meta map[string]any) {
	envelope := map[string]any{
		"success": true,
		"data":    data,
		"meta":    mergeMeta(requestID, meta),
	}
	writeJSON(w, status, envelope)
}

// writeList отдаёт страницу вместе с блоком пагинации: у страницы это часть
// ОТВЕТА, и клиент читает её из meta.
func (s *Server) writeList(w http.ResponseWriter, requestID string, rows []json.RawMessage) {
	s.writeData(w, requestID, mustMarshal(rows), http.StatusOK, map[string]any{
		"total": len(rows), "count": len(rows), "offset": 0, "limit": 20,
		"has_more": false,
		// ⚠ truncated приходит всегда, включая false: «мы посмотрели всё» — это
		// утверждение, а не отсутствие оговорки.
		"truncated": false,
	})
}

func (s *Server) writeError(w http.ResponseWriter, requestID string, code cpa.ErrorCode, message string) {
	status, known := cpa.HTTPStatusFor(code)
	if !known {
		// Код вне реестра — ошибка вызова PrepareError, и она обязана быть видна.
		status = http.StatusInternalServerError
		message = "cpatest: код " + string(code) + " не принадлежит реестру контракта"
	}
	writeJSON(w, status, map[string]any{
		"success": false,
		"error":   map[string]any{"code": string(code), "message": message},
		"meta":    mergeMeta(requestID, nil),
	})
}

func mergeMeta(requestID string, extra map[string]any) map[string]any {
	meta := map[string]any{"request_id": requestID}
	for k, v := range extra {
		meta[k] = v
	}
	return meta
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	raw := mustMarshal(value)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-Id", requestIDOf(value))
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

// requestIDOf достаёт идентификатор из конверта, чтобы он уехал и заголовком:
// клиент предпочитает заголовок, и не прислать его значило бы не проверить этот
// путь вовсе.
func requestIDOf(value any) string {
	envelope, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	meta, ok := envelope["meta"].(map[string]any)
	if !ok {
		return ""
	}
	id, _ := meta["request_id"].(string)
	return id
}

func mustMarshal(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic("cpatest: ответ не собрался: " + err.Error())
	}
	return raw
}

// ── подгонка фикстур под запрос ──────────────────────────────────────────────
//
// ⚠ Ровно подстановка присланных значений, и ничего больше: стенд обязан
// вернуть ТО, о чём его спросили, иначе проверка «ушло то, что ждали» была бы
// зелена на любом запросе.

func patch(raw json.RawMessage, set map[string]any) json.RawMessage {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		panic("cpatest: фикстура не объект: " + err.Error())
	}
	for key, value := range set {
		object[key] = mustMarshal(value)
	}
	return mustMarshal(object)
}

func withStatus(raw json.RawMessage, status string) json.RawMessage {
	return patch(raw, map[string]any{"status": status})
}

// stripTriple убирает из карточки поля, которых НЕТ в проекции списка. Отдавать
// их в списке значило бы проверять клиента на ответе, которого дверь не даёт.
func stripTriple(raw json.RawMessage) json.RawMessage {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		panic("cpatest: фикстура не объект: " + err.Error())
	}
	for _, key := range []string{"revenue_rub", "margin_rub", "agent_leg", "fraud_flags"} {
		delete(object, key)
	}
	return mustMarshal(object)
}

func (s *Server) webhookOf(body []byte) json.RawMessage {
	var in struct {
		URL          string `json:"url"`
		RotateSecret bool   `json:"rotate_secret"`
	}
	_ = json.Unmarshal(body, &in)

	set := map[string]any{"url": in.URL}
	if !in.RotateSecret {
		// ⚠ Секрет показывается при установке и при ротации. Здесь он всегда
		// присутствует — стенд структурный, и «когда именно его показывают»
		// доказывает песочница.
		set["secret"] = "whsec_shown_once"
	} else {
		set["secret"] = "whsec_rotated"
	}
	return patch(fixture("webhook.json"), set)
}

func (s *Server) partnerOf(body []byte) json.RawMessage {
	var in struct {
		ExternalID   string `json:"external_id"`
		Role         string `json:"role"`
		PartnerState string `json:"partner_state"`
		// ⚠ Открепление (null) и «не меняем» (ключа нет) стенд различать не
		// обязан: состояния партнёров он не держит, и в обоих случаях отвечает
		// фикстурой, где рекрутёра нет. Что null действительно уходит в тело,
		// доказывает запись обращения, а не ответ стенда.
		Recruiter string `json:"recruiter_external_id"`
	}
	_ = json.Unmarshal(body, &in)

	set := map[string]any{"external_id": in.ExternalID, "role": in.Role}
	if in.PartnerState != "" {
		set["partner_state"] = in.PartnerState
	}
	if in.Recruiter != "" {
		set["recruiter_external_id"] = in.Recruiter
	}
	return patch(fixture("partner.json"), set)
}

// offerFor выбирает проекцию по параметру view.
//
// ⚠⚠ Умолчание здесь партнёрское — как у двери. Отдай по умолчанию проекцию
// рекламодателя, и тест, забывший параметр, зеленел бы на ответе, который живая
// дверь не даёт.
func (s *Server) offerFor(r *http.Request) json.RawMessage {
	if r.URL.Query().Get("view") == "advertiser" {
		return fixture("offer_advertiser.json")
	}
	return fixture("offer_partner.json")
}

// ── идемпотентность: единственная логика стенда ──────────────────────────────

func (s *Server) acceptConversion(w http.ResponseWriter, body []byte, requestID string) {
	var in struct {
		ExternalID string           `json:"external_id"`
		Goal       *string          `json:"goal"`
		Status     string           `json:"status"`
		SubID      string           `json:"sub_id"`
		SumRUB     *json.RawMessage `json:"sum_rub"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		s.writeError(w, requestID, cpa.CodeInvalidInput, "cpatest: тело не разобрано")
		return
	}

	// Как дверь (normalizeGoal в Recca): нет ключа и пустая строка — цель "1".
	// Разойдись стенд с дверью здесь, Ptr("") давал бы на стенде новую конверсию,
	// а в песочнице — повтор существующей.
	goal := "1"
	if in.Goal != nil && strings.TrimSpace(*in.Goal) != "" {
		goal = strings.TrimSpace(*in.Goal)
	}
	key := in.ExternalID + "\x00" + goal

	s.mu.Lock()
	stored, repeat := s.conversions[key]
	s.mu.Unlock()

	if repeat {
		// ⚠⚠ 200, а не 201: повтор — успех, и это то самое место, где ломается
		// клиент, считающий успехом только 201.
		status := http.StatusOK
		s.mu.Lock()
		if s.repeatStatus != 0 {
			status = s.repeatStatus
		}
		s.mu.Unlock()
		s.writeData(w, requestID, stored, status, nil)
		return
	}

	set := map[string]any{"external_id": in.ExternalID, "goal": goal}
	if in.SubID != "" {
		set["sub_id"] = in.SubID
	}
	record := patch(fixture("conversion.json"), set)
	if in.SumRUB != nil {
		record = patch(record, map[string]any{"sum_rub": *in.SumRUB})
	}

	record = s.applyTamper(record)

	s.mu.Lock()
	s.conversions[key] = record
	s.mu.Unlock()

	s.writeData(w, requestID, record, http.StatusCreated, nil)
}

func (s *Server) acceptPayout(w http.ResponseWriter, body []byte, requestID string) {
	var in struct {
		PartnerExternalID string `json:"partner_external_id"`
		AmountRUB         int64  `json:"amount_rub"`
		PeriodFrom        string `json:"period_from"`
		PeriodTo          string `json:"period_to"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		s.writeError(w, requestID, cpa.CodeInvalidInput, "cpatest: тело не разобрано")
		return
	}

	// ⚠⚠ Ключ — вся четвёрка, БЕЗ суммы: сумма сверяется отдельно. Включи её в
	// ключ — и повтор с другой суммой завёл бы вторую заявку вместо отказа, то
	// есть стенд перестал бы отличать идемпотентность от дубля.
	key := in.PartnerExternalID + "\x00" + in.PeriodFrom + "\x00" + in.PeriodTo

	s.mu.Lock()
	stored, repeat := s.payouts[key]
	refuse := s.refusePayouts
	s.mu.Unlock()

	switch {
	case refuse:
		s.writeError(w, requestID, cpa.CodeInsufficientBalance,
			fmt.Sprintf("cpatest: доступного остатка не хватает на %d ₽", in.AmountRUB))
		return
	case repeat && stored.amount != in.AmountRUB:
		s.writeError(w, requestID, cpa.CodeIdempotentMismatch,
			fmt.Sprintf("заявка с этим ключом создана на %d ₽, а пришло %d ₽", stored.amount, in.AmountRUB))
		return
	case repeat:
		s.writeData(w, requestID, stored.body, http.StatusOK, nil)
		return
	}

	record := patch(fixture("payout.json"), map[string]any{
		"partner_external_id": in.PartnerExternalID,
		"amount_rub":          in.AmountRUB,
		"period_from":         in.PeriodFrom,
		"period_to":           in.PeriodTo,
	})

	s.mu.Lock()
	s.payouts[key] = payoutRecord{amount: in.AmountRUB, body: record}
	s.mu.Unlock()

	s.writeData(w, requestID, record, http.StatusCreated, nil)
}

// balance — баланс партнёра: фикстура либо, под внутренней ручкой canRequest,
// её подмена.
func (s *Server) balance() json.RawMessage {
	s.mu.Lock()
	canRequest := s.canRequest
	s.mu.Unlock()

	raw := fixture("partner_balance.json")
	switch {
	case canRequest == nil:
		return raw
	case *canRequest:
		return patch(raw, map[string]any{"can_request": true})
	default:
		return patch(raw, map[string]any{"can_request": false, "available_rub": 0})
	}
}

// bodyOf — для утверждений в тестах потребителя: тело обращения как строка.
func (r Request) BodyString() string { return string(bytes.TrimSpace(r.Body)) }

// applyTamper пропускает карточку через порчу, если её завёл внутренний тест.
func (s *Server) applyTamper(raw json.RawMessage) json.RawMessage {
	s.mu.Lock()
	tamper := s.tamper
	s.mu.Unlock()
	if tamper == nil {
		return raw
	}
	return tamper(raw)
}
