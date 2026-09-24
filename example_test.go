package cpa_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	"go.recca.ru/cpa"
)

// Приём результата: клик состоялся, заказ подтверждён, дверь возвращает тройку
// ставок и срок холда.
//
// ⚠ Пример исполняемый, поэтому вместо песочницы здесь стенд: пример, который
// ходит в сеть, краснеет от чужих причин — от истёкшего ключа до выключенного
// Wi-Fi, — и его перестают читать как документацию. Настоящий прогон против
// api-sandbox.recca.ru живёт в examples/quickstart.
func Example_createConversion() {
	stand := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"success":true,"data":{
			"external_id":"order-0751","goal":"1","status":"hold",
			"partner_external_id":"blogger-42","offer_id":"off_1","advertiser_id":"adv_1",
			"payout_rub":500,"revenue_rub":800,"margin_rub":300,
			"sum_rub":10000,"sub_id":"tg",
			"created_at":"2026-09-21T00:00:00.000Z","occurred_at":null,
			"hold_until":"2026-09-28T00:00:00.000Z","agent_leg":null,"fraud_flags":[]
		},"meta":{"request_id":"req_01K5EXAMPLE"}}`)
	}))
	defer stand.Close()

	client, err := cpa.New("tk_example_key", &cpa.ClientOptions{BaseURL: stand.URL})
	if err != nil {
		fmt.Println("клиент не создан:", err)
		return
	}

	conversion, response, err := client.Conversions.Create(context.Background(), cpa.CreateConversionParams{
		ClickID:    "clk_01K5",
		ExternalID: "order-0751",
		Status:     "approved",
		Goal:       cpa.Ptr("1"),
		SumRUB:     cpa.RUBPtr(10000),
		SubID:      "tg",
	})
	if err != nil {
		// ⚠ Ветвиться по коду, а не по тексту: сообщение меняется без
		// предупреждения и частью контракта не является.
		fmt.Println("отказ:", err)
		return
	}

	fmt.Printf("статус: %s\n", conversion.Status)
	fmt.Printf("ставки: выплата %s, маржа %s, выручка %s\n",
		conversion.PayoutRUB, conversion.MarginRUB, conversion.RevenueRUB)

	// ⚠ Поля, которые дверь присылает nullʼом, — указатели: hold_until пуст,
	// пока конверсия не подтверждена. Разыменовать его без проверки значит
	// однажды уронить свой сервер на законном ответе.
	if conversion.HoldUntil != nil {
		fmt.Printf("холд до: %s\n", conversion.HoldUntil.Format("2006-01-02"))
	}
	fmt.Printf("HTTP %d, request_id %s\n", response.StatusCode, response.RequestID)

	// Output:
	// статус: hold
	// ставки: выплата 500, маржа 300, выручка 800
	// холд до: 2026-09-28
	// HTTP 201, request_id req_01K5EXAMPLE
}

// Отказ двери различается по коду — и этого достаточно, чтобы решить, что
// делать: повторить, поправить запрос или идти к оператору.
func Example_handleRefusal() {
	stand := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"success":false,"error":{
			"code":"WINDOW_EXPIRED","message":"attribution window has expired"
		},"meta":{"request_id":"req_01K5LATE"}}`)
	}))
	defer stand.Close()

	client, _ := cpa.New("tk_example_key", &cpa.ClientOptions{BaseURL: stand.URL})

	_, _, err := client.Conversions.Create(context.Background(), cpa.CreateConversionParams{
		ClickID: "clk_old", ExternalID: "order-0752", Status: "approved",
	})

	var apiErr *cpa.APIError
	switch {
	case err == nil:
		fmt.Println("принято")
	case errors.Is(err, cpa.ErrWindowExpired):
		fmt.Println("окно атрибуции истекло — задним числом конверсия не засчитывается")
	case errors.As(err, &apiErr) && apiErr.Retryable:
		fmt.Println("временный отказ, стоит повторить:", apiErr.Code)
	default:
		fmt.Println("отказ:", err)
	}

	// Output:
	// окно атрибуции истекло — задним числом конверсия не засчитывается
}
