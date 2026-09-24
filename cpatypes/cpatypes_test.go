// Контракт типов двери: три состояния полей, форма суммы, проверка до сети и
// разбор ответа.
//
// Источник — docs/federation/cpa-api.md (разделы 5–8) и openapi-cpa.yaml.
package cpatypes_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"go.recca.ru/cpa/cpatypes"
)

// ⚠⚠ Раздел 7 контракта: у goal и sum_rub различаются «не передали»,
// «передали пустым» и «передали значение». От различия зависит и ставка, и
// строка подписи, поэтому форма тела закрепляется побайтово.
func TestCreateConversionParamsCarriesThreeStates(t *testing.T) {
	base := cpatypes.CreateConversionParams{ClickID: "clk_1", ExternalID: "order-1", Status: "approved"}

	cases := []struct {
		name   string
		mutate func(*cpatypes.CreateConversionParams)
		want   string
	}{
		{
			name:   "цель не передана — ключа нет",
			mutate: func(p *cpatypes.CreateConversionParams) {},
			want:   `{"click_id":"clk_1","external_id":"order-1","status":"approved"}`,
		},
		{
			name:   "цель передана пустой — ключ есть, значение пусто",
			mutate: func(p *cpatypes.CreateConversionParams) { p.Goal = cpatypes.Ptr("") },
			want:   `{"click_id":"clk_1","external_id":"order-1","status":"approved","goal":""}`,
		},
		{
			name:   "цель названа",
			mutate: func(p *cpatypes.CreateConversionParams) { p.Goal = cpatypes.Ptr("2") },
			want:   `{"click_id":"clk_1","external_id":"order-1","status":"approved","goal":"2"}`,
		},
		{
			name:   "сумма известна и равна нулю",
			mutate: func(p *cpatypes.CreateConversionParams) { p.SumRUB = cpatypes.Ptr(cpatypes.RUB(0)) },
			want:   `{"click_id":"clk_1","external_id":"order-1","status":"approved","sum_rub":0}`,
		},
		{
			name:   "сумма названа — число, а не строка",
			mutate: func(p *cpatypes.CreateConversionParams) { p.SumRUB = cpatypes.Ptr(cpatypes.RUB(10000)) },
			want:   `{"click_id":"clk_1","external_id":"order-1","status":"approved","sum_rub":10000}`,
		},
		{
			name:   "sub_id пуст — совпадает с «не передали»",
			mutate: func(p *cpatypes.CreateConversionParams) { p.SubID = "" },
			want:   `{"click_id":"clk_1","external_id":"order-1","status":"approved"}`,
		},
		{
			name:   "sub_id назван",
			mutate: func(p *cpatypes.CreateConversionParams) { p.SubID = "tg" },
			want:   `{"click_id":"clk_1","external_id":"order-1","status":"approved","sub_id":"tg"}`,
		},
		{
			name: "время события передано",
			mutate: func(p *cpatypes.CreateConversionParams) {
				p.OccurredAt = cpatypes.Ptr(time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC))
			},
			want: `{"click_id":"clk_1","external_id":"order-1","status":"approved","occurred_at":"2026-09-21T00:00:00Z"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			tc.mutate(&p)
			raw, err := json.Marshal(p)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if got := string(raw); got != tc.want {
				t.Errorf("тело:\n  дали  %s\n  ждали %s", got, tc.want)
			}
		})
	}
}

// ⚠⚠ Дробная сумма едет ЛЕКСИЧЕСКОЙ формой: 12.5 обязана остаться 12.5, а не
// стать 12.50 или 12.499999. Денежный тип с собственным форматированием сломал
// бы подпись постбэка молча — вектор sum_fractional закрепляет именно это.
func TestSumRUBExactKeepsLexicalForm(t *testing.T) {
	cases := []string{"12.5", "0.01", "10000000"}
	for _, lexical := range cases {
		t.Run(lexical, func(t *testing.T) {
			p := cpatypes.CreateConversionParams{
				ClickID: "clk_1", ExternalID: "order-1", Status: "approved",
				SumRUBExact: cpatypes.Ptr(json.Number(lexical)),
			}
			raw, err := json.Marshal(p)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			want := `"sum_rub":` + lexical
			if !strings.Contains(string(raw), want) {
				t.Errorf("в теле нет %s:\n  %s", want, raw)
			}
		})
	}
}

// Две двери к одной сумме — это две правды о деньгах. Сказать обе сразу нельзя.
func TestSumRUBAndExactAreExclusive(t *testing.T) {
	p := cpatypes.CreateConversionParams{
		ClickID: "clk_1", ExternalID: "order-1", Status: "approved",
		SumRUB:      cpatypes.Ptr(cpatypes.RUB(100)),
		SumRUBExact: cpatypes.Ptr(json.Number("12.5")),
	}
	err := p.Validate()
	if err == nil {
		t.Fatal("две формы суммы приняты разом")
	}
	var v *cpatypes.ValidationError
	if !errors.As(err, &v) || v.Field != "sum_rub" {
		t.Errorf("ошибка = %v, ждали ValidationError про sum_rub", err)
	}
}

// Проверка до сети: отказ, который мы можем дать сами, не стоит ни запроса, ни
// чужого кода ошибки. ⚠ Список — только про ФОРМУ: смысл значения (не прислали
// ли сюда телефон клиента) проверяет дверь, и заменять её проверку своей
// значило бы обещать то, чего клиент не исполняет.
func TestValidateRejectsMalformedInput(t *testing.T) {
	valid := cpatypes.CreateConversionParams{ClickID: "clk_1", ExternalID: "order-1", Status: "approved"}

	cases := []struct {
		name      string
		mutate    func(*cpatypes.CreateConversionParams)
		wantField string
	}{
		{"нет click_id", func(p *cpatypes.CreateConversionParams) { p.ClickID = "" }, "click_id"},
		{"нет external_id", func(p *cpatypes.CreateConversionParams) { p.ExternalID = "" }, "external_id"},
		{"нет status", func(p *cpatypes.CreateConversionParams) { p.Status = "" }, "status"},
		{
			"external_id длиннее предела",
			func(p *cpatypes.CreateConversionParams) {
				p.ExternalID = strings.Repeat("x", cpatypes.MaxExternalIDLen+1)
			},
			"external_id",
		},
		{
			// ⚠ Пространство ключей платформы. Столкновение молчаливо в ОБЕ
			// стороны: занятый ключ превратил бы нашу операцию в идемпотентное
			// попадание, и наоборот.
			"external_id в зарезервированном пространстве",
			func(p *cpatypes.CreateConversionParams) { p.ExternalID = cpatypes.ReservedExternalIDPrefix + "order-1" },
			"external_id",
		},
		{
			"sub_id длиннее предела",
			func(p *cpatypes.CreateConversionParams) { p.SubID = strings.Repeat("s", cpatypes.MaxSubIDLen+1) },
			"sub_id",
		},
		{
			"goal длиннее предела",
			func(p *cpatypes.CreateConversionParams) {
				p.Goal = cpatypes.Ptr(strings.Repeat("g", cpatypes.MaxGoalLen+1))
			},
			"goal",
		},
		{
			"сумма отрицательная",
			func(p *cpatypes.CreateConversionParams) { p.SumRUB = cpatypes.Ptr(cpatypes.RUB(-1)) },
			"sum_rub",
		},
		{
			"сумма выше потолка контракта",
			func(p *cpatypes.CreateConversionParams) { p.SumRUB = cpatypes.Ptr(cpatypes.MaxSumRUB + 1) },
			"sum_rub",
		},
		{
			"точная сумма не является числом",
			func(p *cpatypes.CreateConversionParams) { p.SumRUBExact = cpatypes.Ptr(json.Number("12,5")) },
			"sum_rub",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := valid
			tc.mutate(&p)
			err := p.Validate()
			if err == nil {
				t.Fatal("значение принято")
			}
			var v *cpatypes.ValidationError
			if !errors.As(err, &v) {
				t.Fatalf("ошибка не *ValidationError: %T (%v)", err, err)
			}
			if v.Field != tc.wantField {
				t.Errorf("поле %q, ждали %q", v.Field, tc.wantField)
			}
			if !errors.Is(err, cpatypes.ErrValidation) {
				t.Error("errors.Is(err, ErrValidation) = false")
			}
		})
	}
}

// ⚠ Запрет обязан доказать разрешение: набор из одних отказов был бы зелёным и
// у проверки, которая не принимает НИЧЕГО.
func TestValidateAcceptsContractShapes(t *testing.T) {
	cases := []struct {
		name string
		p    cpatypes.CreateConversionParams
	}{
		{"минимальный набор", cpatypes.CreateConversionParams{ClickID: "clk_1", ExternalID: "order-1", Status: "approved"}},
		{
			"полный набор",
			cpatypes.CreateConversionParams{
				ClickID: "clk_1", ExternalID: "order-1", Status: "approved",
				Goal: cpatypes.Ptr("2"), SumRUB: cpatypes.Ptr(cpatypes.RUB(10000)), SubID: "tg",
				OccurredAt: cpatypes.Ptr(time.Now().Add(-time.Hour)),
			},
		},
		{
			"сумма ровно на потолке",
			cpatypes.CreateConversionParams{ClickID: "clk_1", ExternalID: "order-1", Status: "approved", SumRUB: cpatypes.Ptr(cpatypes.MaxSumRUB)},
		},
		{
			"сумма ноль — известна и равна нулю",
			cpatypes.CreateConversionParams{ClickID: "clk_1", ExternalID: "order-1", Status: "approved", SumRUB: cpatypes.Ptr(cpatypes.RUB(0))},
		},
		{
			"дробная сумма лексической формой",
			cpatypes.CreateConversionParams{ClickID: "clk_1", ExternalID: "order-1", Status: "approved", SumRUBExact: cpatypes.Ptr(json.Number("12.5"))},
		},
		{
			"external_id длиной ровно в предел",
			cpatypes.CreateConversionParams{ClickID: "clk_1", ExternalID: strings.Repeat("x", cpatypes.MaxExternalIDLen), Status: "approved"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.p.Validate(); err != nil {
				t.Errorf("отвергнуто законное: %v", err)
			}
		})
	}
}

// Идентификатор партнёра назначает интегратор, а алфавит и длину — контракт
// (раздел 5). Проверка до сети называет поле, а не отдаёт чужой 400.
func TestPartnerExternalIDValidate(t *testing.T) {
	cases := []struct {
		name  string
		value cpatypes.PartnerExternalID
		ok    bool
	}{
		{"обычный", "blogger-42", true},
		{"все разрешённые знаки", "A_b.9:x-Z", true},
		{"ровно предел", cpatypes.PartnerExternalID(strings.Repeat("a", cpatypes.MaxPartnerExternalIDLen)), true},
		{"пустой", "", false},
		{"длиннее предела", cpatypes.PartnerExternalID(strings.Repeat("a", cpatypes.MaxPartnerExternalIDLen+1)), false},
		{"пробел внутри", "blogger 42", false},
		{"кириллица", "блогер", false},
		{"слеш", "blogger/42", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.value.Validate()
			if tc.ok && err != nil {
				t.Errorf("отвергнут законный %q: %v", tc.value, err)
			}
			if !tc.ok && err == nil {
				t.Errorf("принят недопустимый %q", tc.value)
			}
		})
	}
}

// ⚠⚠ truncated приходит ВСЕГДА, в том числе со значением false: «мы посмотрели
// всё» — утверждение, а не отсутствие оговорки. Значит «не прислали» и «прислали
// false» обязаны различаться, иначе неполная выдача читалась бы как полная.
func TestMetaTruncatedHasThreeStates(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want *bool
	}{
		{"поля нет", `{"request_id":"req_1"}`, nil},
		{"посмотрели всё", `{"request_id":"req_1","truncated":false}`, cpatypes.Ptr(false)},
		{"выдача неполна", `{"request_id":"req_1","truncated":true}`, cpatypes.Ptr(true)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m cpatypes.Meta
			if err := json.Unmarshal([]byte(tc.raw), &m); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			switch {
			case tc.want == nil && m.Truncated != nil:
				t.Errorf("Truncated = %v, ждали «поля не было»", *m.Truncated)
			case tc.want != nil && m.Truncated == nil:
				t.Errorf("Truncated = nil, ждали %v", *tc.want)
			case tc.want != nil && *m.Truncated != *tc.want:
				t.Errorf("Truncated = %v, ждали %v", *m.Truncated, *tc.want)
			}
		})
	}
}

// Ответ двери разбирается без потери точности суммы и без подмены «не
// прислали» нулём.
func TestConversionDetailUnmarshal(t *testing.T) {
	const raw = `{
		"external_id":"order-0751","goal":"1","status":"hold",
		"partner_external_id":"blogger-42","offer_id":"off_1","advertiser_id":"adv_1",
		"payout_rub":500,"revenue_rub":800,"margin_rub":300,
		"sum_rub":12.5,"sub_id":null,
		"created_at":"2026-09-21T00:00:00.000Z","occurred_at":null,"hold_until":"2026-09-28T00:00:00.000Z",
		"agent_leg":{"external_id":"agent-1","amount_rub":60,"base":"margin","percent":20,"hold_until":null},
		"fraud_flags":["ip_repeat"]
	}`

	var c cpatypes.ConversionDetail
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if c.Status != cpatypes.ConversionHold {
		t.Errorf("Status = %q, ждали %q", c.Status, cpatypes.ConversionHold)
	}
	if c.SumRUB == nil || c.SumRUB.String() != "12.5" {
		t.Errorf("SumRUB = %v, ждали лексическую форму 12.5", c.SumRUB)
	}
	if c.SubID != nil {
		t.Errorf("SubID = %v, ждали nil: null и пустая строка — разные ответы", *c.SubID)
	}
	if c.OccurredAt != nil {
		t.Error("OccurredAt = не nil, а дверь прислала null («не сказали, когда»)")
	}
	if c.HoldUntil == nil {
		t.Error("HoldUntil = nil, а дверь назвала время")
	}
	// ⚠ Тройка ставок сходится — это и есть доказательство приёмки по п. 3.4
	// договора; тип обязан донести все три, а не только выплату.
	if c.PayoutRUB+c.MarginRUB != c.RevenueRUB {
		t.Errorf("payout %d + margin %d != revenue %d", c.PayoutRUB, c.MarginRUB, c.RevenueRUB)
	}
	if c.AgentLeg == nil {
		t.Fatal("AgentLeg = nil, а в ответе нога рекрутёра есть")
	}
	if c.AgentLeg.Base != "margin" || c.AgentLeg.AmountRUB != 60 {
		t.Errorf("AgentLeg = %+v", *c.AgentLeg)
	}
	if c.AgentLeg.HoldUntil != nil {
		t.Error("AgentLeg.HoldUntil обязан быть nil: null значит «значения не существует»")
	}
	if len(c.FraudFlags) != 1 || c.FraudFlags[0] != "ip_repeat" {
		t.Errorf("FraudFlags = %v", c.FraudFlags)
	}
}

// Наследие LOOP-01, обобщённое: «спека обещает N полей — код на меньшее не
// соглашается». Недостающее поле json.Unmarshal молча оставил бы нулём, и
// потребитель увидел бы выдуманные суммы, которых дверь не присылала.
//
// ⚠⚠ Обязательность — свойство КОНТРАКТА, а не всех полей подряд: у конверсии
// `sub_id` и `occurred_at` законно приходят null. Поэтому требуются только поля
// с тегом `cpa:"required"`, и список обязательного живёт в самом типе — одной
// копией, рядом с полем.
func TestRequireFields(t *testing.T) {
	type sample struct {
		A int    `json:"a" cpa:"required"`
		B string `json:"b" cpa:"required"`
		C bool   `json:"c" cpa:"required"`
		// Необязательное по контракту: null здесь законен.
		F string `json:"f"`
		// Не часть контракта вовсе.
		D string `json:"-"`
		E string
	}

	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"обязательные на месте", `{"a":1,"b":"x","c":false}`, false},
		{"нули — законные значения", `{"a":0,"b":"","c":false}`, false},
		{"посторонние поля рядом не мешают", `{"a":1,"b":"x","c":true,"z":9}`, false},
		{"необязательное поле пришло null", `{"a":1,"b":"x","c":true,"f":null}`, false},
		{"обязательного поля нет", `{"a":1,"c":true}`, true},
		{"вместо обязательного null", `{"a":1,"b":null,"c":true}`, true},
		{"объект пуст", `{}`, true},
		{"вместо объекта null", `null`, true},
		{"данных нет вовсе", ``, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := cpatypes.RequireFields[sample]([]byte(tc.raw))
			if tc.wantErr && err == nil {
				t.Fatal("неполные данные приняты — наружу ушёл бы выдуманный ноль")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("полные данные отвергнуты: %v", err)
			}
			if tc.wantErr && err != nil && strings.TrimSpace(tc.raw) == `{"a":1,"c":true}` && !strings.Contains(err.Error(), "b") {
				t.Errorf("ошибка не называет недостающее поле: %v", err)
			}
		})
	}
}

// ⚠⚠ Проверка, которой нечего проверять, зелена ПО ПУСТОТЕ: тип без ни одного
// `cpa:"required"` пропустил бы любой ответ, и выглядело бы это как строгий
// разбор. Такой вызов — ошибка использования, и он обязан быть громким.
func TestRequireFieldsRefusesTypeWithoutRequired(t *testing.T) {
	type nothingRequired struct {
		A int    `json:"a"`
		B string `json:"b"`
	}

	err := cpatypes.RequireFields[nothingRequired]([]byte(`{"a":1,"b":"x"}`))
	if err == nil {
		t.Fatal("тип без обязательных полей принят — проверка была бы пустой")
	}
	if errors.Is(err, cpatypes.ErrIncompleteData) {
		t.Error("это ошибка ВЫЗОВА, а не ответа двери: путать их значит искать дефект не там")
	}
}

// Обязательные поля встроенной структуры — тоже обязательные: ConversionDetail
// встраивает Conversion, и набор у него общий.
func TestRequireFieldsWalksEmbedded(t *testing.T) {
	type inner struct {
		A int `json:"a" cpa:"required"`
	}
	type outer struct {
		inner
		B string `json:"b" cpa:"required"`
	}

	if err := cpatypes.RequireFields[outer]([]byte(`{"a":1,"b":"x"}`)); err != nil {
		t.Fatalf("полные данные отвергнуты: %v", err)
	}
	err := cpatypes.RequireFields[outer]([]byte(`{"b":"x"}`))
	if err == nil {
		t.Fatal("поле встроенной структуры не потребовано")
	}
	if !strings.Contains(err.Error(), "a") {
		t.Errorf("ошибка не называет поле встроенной структуры: %v", err)
	}
}

// Страница проверяется построчно.
//
// ⚠⚠ Именно КАЖДАЯ строка, а не первая: «первая полная, третья усечённая» —
// отказ, который доезжает до интерфейса молча, и выборочная проверка зелена на
// нём по той же причине, по которой зелен пустой тест.
func TestRequireFieldsEach(t *testing.T) {
	type sample struct {
		A int    `json:"a" cpa:"required"`
		B string `json:"b" cpa:"required"`
	}

	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"все строки полные", `[{"a":1,"b":"x"},{"a":2,"b":"y"}]`, false},
		{"пустая страница законна", `[]`, false},
		{"первая строка неполна", `[{"a":1},{"a":2,"b":"y"}]`, true},
		{"ПОСЛЕДНЯЯ строка неполна", `[{"a":1,"b":"x"},{"a":2,"b":"y"},{"a":3}]`, true},
		{"в строке вместо обязательного null", `[{"a":1,"b":null}]`, true},
		{"вместо массива null", `null`, true},
		{"данных нет вовсе", ``, true},
		{"вместо массива объект", `{"a":1,"b":"x"}`, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := cpatypes.RequireFieldsEach[sample]([]byte(tc.raw))
			if tc.wantErr && err == nil {
				t.Fatal("неполная страница принята — наружу ушли бы выдуманные нули")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("полная страница отвергнута: %v", err)
			}
		})
	}

	// Номер строки в ошибке нужен затем же, зачем request_id: без него у страницы
	// на сотню строк непонятно, какую смотреть.
	err := cpatypes.RequireFieldsEach[sample]([]byte(`[{"a":1,"b":"x"},{"a":2,"b":"y"},{"a":3}]`))
	if err == nil {
		t.Fatal("усечённая строка принята")
	}
	if !strings.Contains(err.Error(), "строка 2") || !strings.Contains(err.Error(), "b") {
		t.Errorf("ошибка не называет ни строку, ни поле: %v", err)
	}
	if !errors.Is(err, cpatypes.ErrIncompleteData) {
		t.Errorf("ошибка = %v, ждали ErrIncompleteData", err)
	}
}

// Пустая проверка не становится осмысленной от того, что применена к странице:
// тип без cpa:"required" пропустил бы любой массив.
func TestRequireFieldsEachRefusesTypeWithoutRequired(t *testing.T) {
	type nothingRequired struct {
		A int `json:"a"`
	}

	err := cpatypes.RequireFieldsEach[nothingRequired]([]byte(`[{"a":1}]`))
	if err == nil {
		t.Fatal("тип без обязательных полей принят — проверка была бы пустой")
	}
	if errors.Is(err, cpatypes.ErrIncompleteData) {
		t.Error("это ошибка ВЫЗОВА, а не ответа двери")
	}
}

// Ответ двери разбирается строго: у конверсии обязательны те поля, что названы
// обязательными в контракте, и nullable среди них нет.
func TestConversionDetailRequiredFields(t *testing.T) {
	const full = `{"external_id":"order-1","goal":"1","status":"hold","created_at":"2026-09-21T00:00:00.000Z",
		"revenue_rub":800,"margin_rub":300,"payout_rub":500,
		"partner_external_id":null,"sum_rub":null,"sub_id":null,"occurred_at":null,"hold_until":null,"agent_leg":null}`

	if err := cpatypes.RequireFields[cpatypes.ConversionDetail]([]byte(full)); err != nil {
		t.Fatalf("законный ответ с null в необязательных полях отвергнут: %v", err)
	}

	// ⚠ Запрет доказывает разрешение только парой: выкинем обязательное поле.
	//
	// ⚠⚠ payout_rub в этом списке — по ЗАМЕРУ КОДА платформы, а не по openapi:
	// в `required` схемы Conversion его нет, но проектор ставит его всегда и не
	// nullʼом (`utils/cpa-api-projection.ts:497`). Молчаливый ноль именно здесь
	// — сумма выплаты партнёру.
	for _, missing := range []string{"revenue_rub", "margin_rub", "payout_rub", "status", "created_at"} {
		t.Run("нет "+missing, func(t *testing.T) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(full), &fields); err != nil {
				t.Fatalf("фикстура не разобрана: %v", err)
			}
			delete(fields, missing)
			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if err := cpatypes.RequireFields[cpatypes.ConversionDetail](raw); err == nil {
				t.Errorf("ответ без %s принят — наружу ушёл бы ноль вместо суммы", missing)
			}
		})
	}
}

// WV-0770: у рекрутёра три состояния двери — ключа нет (не меняем), строка
// (прикрепить) и null (открепить). Каждое обязано доехать до тела ровно собой.
func TestUpsertPartnerRecruiterThreeStates(t *testing.T) {
	cases := []struct {
		name   string
		params cpatypes.UpsertPartnerParams
		want   string
	}{
		{"не меняем — ключа нет",
			cpatypes.UpsertPartnerParams{ExternalID: "blg-1", Role: cpatypes.PartnerWebmaster},
			`{"external_id":"blg-1","role":"webmaster"}`},
		{"прикрепить — строка",
			cpatypes.UpsertPartnerParams{ExternalID: "blg-1", Role: cpatypes.PartnerWebmaster, RecruiterExternalID: "scout-1"},
			`{"external_id":"blg-1","role":"webmaster","recruiter_external_id":"scout-1"}`},
		{"открепить — явный null",
			cpatypes.UpsertPartnerParams{ExternalID: "blg-1", Role: cpatypes.PartnerWebmaster, DetachRecruiter: true},
			`{"external_id":"blg-1","role":"webmaster","recruiter_external_id":null}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.params.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			got, err := json.Marshal(tc.params)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("тело %s, ждали %s", got, tc.want)
			}
		})
	}
}

// Прикрепить и открепить разом — не операция двери, а ошибка вызывающего: отказ
// до сети, с полем, а не молчаливая победа одного из двух.
func TestUpsertPartnerDetachAndRecruiterAreExclusive(t *testing.T) {
	err := cpatypes.UpsertPartnerParams{
		ExternalID: "blg-1", Role: cpatypes.PartnerWebmaster,
		RecruiterExternalID: "scout-1", DetachRecruiter: true,
	}.Validate()
	if err == nil {
		t.Fatal("оба поля сразу прошли проверку")
	}
	if !strings.Contains(err.Error(), "recruiter_external_id") {
		t.Errorf("отказ не называет поле: %v", err)
	}
}
