package cpa_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.recca.ru/cpa"
	"go.recca.ru/cpa/cpatest"
)

// Примеры по сервисам исполняются против стенда cpatest — детерминированно и без
// сети. ⚠ Числа в выводе — фикстуры стенда, а не поведение платформы: стенд
// доказывает форму запроса и разбор ответа, тройку ставок и холд доказывает
// песочница (см. README, «Граница моков»).

// standClient — клиент на стенд с выключенными повторами, как Server.Client.
func standClient(stand *cpatest.Server) *cpa.Client {
	client, err := cpa.New("tk_example_key", &cpa.ClientOptions{
		BaseURL:    stand.URL(),
		MaxRetries: cpa.Ptr(0),
	})
	if err != nil {
		panic(err)
	}
	return client
}

// rateText печатает ставку. ⚠ Заполнено ровно одно из двух значений — по Kind:
// у процентной ставки ValueRUB равен nil, и разыменовать его вслепую значит
// уронить программу на законном ответе.
func rateText(r cpa.Rate) string {
	switch {
	case r.ValueRUB != nil:
		return r.ValueRUB.String() + " ₽"
	case r.ValuePercent != nil:
		return r.ValuePercent.String() + " %"
	default:
		return "не задана"
	}
}

// valueOr — значение указателя или запасное, если дверь прислала null.
func valueOr(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return *p
}

// Self.Get — проба живости и настройки сети. Бюджет запросов берите отсюда, а не
// из документации: это то самое число, по которому дверь решает о 429.
func ExampleSelfService_Get() {
	stand := cpatest.Start()
	defer stand.Close()
	client := standClient(stand)

	self, _, err := client.Self.Get(context.Background())
	if err != nil {
		fmt.Println("отказ:", err)
		return
	}
	// Среду называет сервер: тестовые данные пишите только туда, где здесь sandbox.
	fmt.Println("среда:", self.Environment)
	fmt.Println("контракт:", self.ContractVersion)
	fmt.Println("бюджет в минуту:", self.RateLimitPerMin)
	fmt.Println("нога рекрутёра считается от:", self.AgentBase)

	// Output:
	// среда: sandbox
	// контракт: 2026-09-24
	// бюджет в минуту: 3000
	// нога рекрутёра считается от: margin
}

// Self.SetWebhook — адрес событий. Секрет подписи показывается ОДИН раз: при
// первой установке и при ротации.
//
// ⚠ Метод не повторяется при сбое сам. Не получили ответа — вызовите снова с
// RotateSecret: true: простой повтор вернул бы успех без секрета.
func ExampleSelfService_SetWebhook() {
	stand := cpatest.Start()
	defer stand.Close()
	client := standClient(stand)

	hook, _, err := client.Self.SetWebhook(context.Background(), cpa.SetWebhookParams{
		URL: "https://hook.example/cpa",
	})
	if err != nil {
		fmt.Println("отказ:", err)
		return
	}
	if hook.Secret != nil {
		fmt.Println("секрет получен — сохраните его сейчас")
	}
	fmt.Println("событий:", len(hook.Events))

	// Output:
	// секрет получен — сохраните его сейчас
	// событий: 4
}

// Partners.Upsert — завести партнёра со своим идентификатором. И 201, и 200 —
// успех: второе значит «уже был».
func ExamplePartnersService_Upsert() {
	stand := cpatest.Start()
	defer stand.Close()
	client := standClient(stand)

	partner, response, err := client.Partners.Upsert(context.Background(), cpa.UpsertPartnerParams{
		ExternalID: "blogger-42",
		Role:       cpa.PartnerWebmaster,
	})
	if err != nil {
		// ⚠ На уже заведённом партнёре другая роль — 400 INVALID_INPUT: роль
		// меняет оператор. См. README, «Ловушки».
		fmt.Println("отказ:", err)
		return
	}
	fmt.Println(partner.ExternalID, partner.Role, partner.Status)
	fmt.Println("заведён впервые:", response.StatusCode == 201)

	// Output:
	// blogger-42 webmaster active
	// заведён впервые: true
}

// Offers — две проекции, два имени метода. Партнёру — его ставка, рекламодателю —
// выручка; одна проекция другую не содержит.
func ExampleOffersService_ListForPartner() {
	stand := cpatest.Start()
	defer stand.Close()
	client := standClient(stand)

	offers, _, err := client.Offers.ListForPartner(context.Background(), cpa.ListOffersParams{})
	if err != nil {
		fmt.Println("отказ:", err)
		return
	}
	for _, offer := range offers {
		fmt.Printf("%s: %s, партнёру %s\n", offer.ID, offer.Name, rateText(offer.PayoutRate))
	}

	// Output:
	// off_1: Подписка на курс, партнёру 500 ₽
}

// Проекция рекламодателя требует рекламодателя — аргументом, а не полем фильтра.
func ExampleOffersService_ListForAdvertiser() {
	stand := cpatest.Start()
	defer stand.Close()
	client := standClient(stand)

	offers, _, err := client.Offers.ListForAdvertiser(context.Background(), "adv_1", cpa.ListOffersParams{})
	if err != nil {
		fmt.Println("отказ:", err)
		return
	}
	for _, offer := range offers {
		fmt.Printf("%s: выручка %s\n", offer.ID, rateText(offer.RevenueRate))
	}

	// Без рекламодателя — отказ до сети, с именем поля.
	_, _, err = client.Offers.ListForAdvertiser(context.Background(), "", cpa.ListOffersParams{})
	fmt.Println("без рекламодателя:", errors.Is(err, cpa.ErrValidation))

	// Output:
	// off_1: выручка 800 ₽
	// без рекламодателя: true
}

// Links.Create — брендированная ссылка партнёра на оффер. Повтор той же пары
// возвращает ту же ссылку, поэтому метод повторяется при сбое сам.
func ExampleLinksService_Create() {
	stand := cpatest.Start()
	defer stand.Close()
	client := standClient(stand)

	link, _, err := client.Links.Create(context.Background(), cpa.CreateLinkParams{
		PartnerExternalID: "blogger-42",
		OfferID:           "off_1",
	})
	if err != nil {
		fmt.Println("отказ:", err)
		return
	}
	fmt.Println(link.Code, link.TargetURL)

	// Output:
	// lnk_7f3a https://api-sandbox.recca.ru/go/lnk_7f3a
}

// Clicks.Create — переход. Отбитый переход (trafficback) — не ошибка: запрос
// верный, а кап исчерпан или гео не подходит. Тогда ClickID и RedirectURL — nil.
//
// ⚠ Метод не повторяется при сбое сам: повтор — второй переход и лишний расход
// капа. Дедупликацию держит ваш ClientClickID.
func ExampleClicksService_Create() {
	stand := cpatest.Start()
	defer stand.Close()
	client := standClient(stand)

	click, _, err := client.Clicks.Create(context.Background(), cpa.CreateClickParams{
		PartnerExternalID: "blogger-42",
		OfferID:           "off_1",
		ClientClickID:     "tap-7c1e",
	})
	if err != nil {
		fmt.Println("отказ:", err)
		return
	}
	switch {
	case click.Status == cpa.ClickTrafficback:
		// reason приходит null, если причину дверь не записала.
		fmt.Println("переход отбит:", valueOr(click.Reason, "без причины"))
	case click.ClickID != nil:
		fmt.Println("клик:", *click.ClickID)
		if click.RedirectURL != nil {
			fmt.Println("вести на:", *click.RedirectURL)
		}
	}

	// Output:
	// клик: clk_a91a75a6ba175cbb
	// вести на: https://adv.example/course?c=clk_a91a75a6ba175cbb
}

// Conversions.Create — повтор с той же тройкой «сеть + external_id + цель»
// возвращает ту же конверсию с 200. Считать успехом только 201 — ошибка.
func ExampleConversionsService_Create() {
	stand := cpatest.Start()
	defer stand.Close()
	client := standClient(stand)

	params := cpa.CreateConversionParams{
		ClickID:    "clk_a91a75a6ba175cbb",
		ExternalID: "order-0751",
		Status:     "approved",
		SumRUB:     cpa.RUBPtr(2500),
	}
	for attempt := 1; attempt <= 2; attempt++ {
		conversion, response, err := client.Conversions.Create(context.Background(), params)
		if err != nil {
			fmt.Println("отказ:", err)
			return
		}
		fmt.Printf("попытка %d: HTTP %d, %s, выплата %s\n",
			attempt, response.StatusCode, conversion.ExternalID, conversion.PayoutRUB)
	}

	// Output:
	// попытка 1: HTTP 201, order-0751, выплата 500
	// попытка 2: HTTP 200, order-0751, выплата 500
}

// Conversions.Get — конверсия адресуется тройкой «сеть + номер заказа + цель».
// Goal == nil и Ptr("") — цель по умолчанию "1"; другую цель называют явно.
func ExampleConversionsService_Get() {
	stand := cpatest.Start()
	defer stand.Close()
	client := standClient(stand)

	conversion, _, err := client.Conversions.Get(context.Background(), cpa.GetConversionParams{
		ExternalID: "order-1",
		Goal:       cpa.Ptr("1"),
	})
	if err != nil {
		fmt.Println("отказ:", err)
		return
	}
	fmt.Println(conversion.ExternalID, conversion.Status)
	fmt.Println("сходится:", conversion.PayoutRUB+conversion.MarginRUB == conversion.RevenueRUB)

	// Output:
	// order-1 hold
	// сходится: true
}

// Conversions.Cancel — отмена терминальна, а её повтор безопасен: метод
// повторяется при сбое сам.
func ExampleConversionsService_Cancel() {
	stand := cpatest.Start()
	defer stand.Close()
	client := standClient(stand)

	conversion, _, err := client.Conversions.Cancel(context.Background(), cpa.CancelConversionParams{
		ExternalID: "order-1",
		Reason:     "возврат заказа",
	})
	if err != nil {
		fmt.Println("отказ:", err)
		return
	}
	fmt.Println(conversion.ExternalID, conversion.Status)

	// Output:
	// order-1 declined
}

// Payouts.Create — личность заявки — партнёр и период; сумма СВЕРЯЕТСЯ. Та же
// заявка с другой суммой — 409 IDEMPOTENT_MISMATCH, а не вторая выплата.
func ExamplePayoutsService_Create() {
	stand := cpatest.Start()
	defer stand.Close()
	client := standClient(stand)

	from, to := cpa.NewDate(2026, time.September, 1), cpa.NewDate(2026, time.September, 30)

	first, err := cpa.NewPayoutParams("blogger-42", 1500, from, to)
	if err != nil {
		fmt.Println("заявка не собрана:", err)
		return
	}
	payout, _, err := client.Payouts.Create(context.Background(), first)
	if err != nil {
		fmt.Println("отказ:", err)
		return
	}
	fmt.Println("заявка:", payout.AmountRUB, payout.Status)

	other, _ := cpa.NewPayoutParams("blogger-42", 2000, from, to)
	_, _, err = client.Payouts.Create(context.Background(), other)
	fmt.Println("другая сумма за тот же период:", errors.Is(err, cpa.ErrIdempotentMismatch))

	// Output:
	// заявка: 1500 requested
	// другая сумма за тот же период: true
}

// Statistics.Get — отчёт в выбранном разрезе. День конверсии — время события
// (occurred_at), а если его не прислали — время приёма.
func ExampleStatisticsService_Get() {
	stand := cpatest.Start()
	defer stand.Close()
	client := standClient(stand)

	rows, _, err := client.Statistics.Get(context.Background(), cpa.StatisticsParams{
		GroupBy: cpa.StatisticsByOffer,
	})
	if err != nil {
		fmt.Println("отказ:", err)
		return
	}
	for _, row := range rows {
		fmt.Printf("%s: кликов %d, конверсий %d, выплата %s\n", row.Key, row.Clicks, row.Conversions, row.PayoutRUB)
	}

	// Output:
	// off_1: кликов 120, конверсий 8, выплата 4000
	// off_2: кликов 40, конверсий 1, выплата 0
}

// Ветка отказа через стенд: PrepareError заготавливает отказ на ОДИН следующий
// запрос. Годится для клиента без повторов (как Server.Client).
func Example_preparedRefusal() {
	stand := cpatest.Start()
	defer stand.Close()
	client := standClient(stand)

	stand.PrepareError(cpa.CodePartnerNotActive)

	_, _, err := client.Clicks.Create(context.Background(), cpa.CreateClickParams{
		PartnerExternalID: "blogger-42",
		OfferID:           "off_1",
	})
	if errors.Is(err, cpa.ErrPartnerNotActive) {
		fmt.Println("партнёр не действует — переход не засчитан")
	}

	// Следующий запрос уже обычный.
	_, _, err = client.Clicks.Create(context.Background(), cpa.CreateClickParams{
		PartnerExternalID: "blogger-42",
		OfferID:           "off_1",
	})
	fmt.Println("второй запрос успешен:", err == nil)

	// Output:
	// партнёр не действует — переход не засчитан
	// второй запрос успешен: true
}

// HoldError — отказ на КАЖДОМ запросе до ClearError. Он нужен клиенту С
// повторами: одноразовый PrepareError на повторяемом коде съела бы собственная
// политика повтора, и тест ветки отказа зеленел бы по неправильной причине.
func Example_heldRefusal() {
	stand := cpatest.Start()
	defer stand.Close()

	// Свой клиент с одним повтором — так устроен боевой.
	client, _ := cpa.New("tk_example_key", &cpa.ClientOptions{
		BaseURL:    stand.URL(),
		MaxRetries: cpa.Ptr(1),
	})

	stand.HoldError(cpa.CodeInternalError)
	_, _, err := client.Self.Get(context.Background())
	fmt.Println("отказ дошёл до вызывающего:", errors.Is(err, cpa.ErrInternal))
	fmt.Println("запросов увидел стенд:", len(stand.Requests()))

	stand.ClearError()
	_, _, err = client.Self.Get(context.Background())
	fmt.Println("после ClearError:", err == nil)

	// Output:
	// отказ дошёл до вызывающего: true
	// запросов увидел стенд: 2
	// после ClearError: true
}
