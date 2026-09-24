// Квикстарт: переход и приём результата в песочнице Recca.
//
// Прогон:
//
//	RECCA_CPA_KEY=<ключ арендатора песочницы> \
//	RECCA_CPA_PARTNER=<ваш идентификатор партнёра> \
//	RECCA_CPA_OFFER=<оффер из каталога> \
//	RECCA_CPA_EXTERNAL_ID=<номер заказа> \
//	go run ./examples/quickstart
//
// ⚠ Ключ живёт на СЕРВЕРЕ и в браузер пользователя не попадает: один ключ
// адресует любого партнёра сети, и разграничение по людям — работа интегратора.
//
// ⚠ Программа создаёт в песочнице клик и конверсию. Номер заказа общий для всей
// сети: повторный запуск с тем же номером вернёт ту же конверсию с HTTP 200.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"go.recca.ru/cpa"
)

func main() {
	ctx := context.Background()

	// ⚠ Environment указан явно: нулевое значение — это боевая сеть.
	client, err := cpa.New(os.Getenv("RECCA_CPA_KEY"), &cpa.ClientOptions{Environment: cpa.Sandbox})
	if err != nil {
		log.Fatalf("клиент не создан: %v", err)
	}

	// Переход. Clicks.Create сам не повторяется — повтор был бы вторым переходом.
	// Свой ClientClickID делает повтор безопасным: дверь вернёт тот же клик.
	click, _, err := client.Clicks.Create(ctx, cpa.CreateClickParams{
		PartnerExternalID: cpa.PartnerExternalID(os.Getenv("RECCA_CPA_PARTNER")),
		OfferID:           os.Getenv("RECCA_CPA_OFFER"),
		ClientClickID:     "quickstart-" + os.Getenv("RECCA_CPA_EXTERNAL_ID"),
	})
	if err != nil {
		log.Fatalf("переход не принят: %v", err)
	}
	if click.Status == cpa.ClickTrafficback || click.ClickID == nil {
		// Не ошибка: кап исчерпан или гео не подходит. Клика нет — засчитывать нечего.
		// ⚠ reason законно приходит null — указатель проверяется.
		reason := "без причины"
		if click.Reason != nil {
			reason = *click.Reason
		}
		log.Fatalf("переход отбит: %s", reason)
	}

	conversion, response, err := client.Conversions.Create(ctx, cpa.CreateConversionParams{
		ClickID:    *click.ClickID,
		ExternalID: os.Getenv("RECCA_CPA_EXTERNAL_ID"),
		Status:     "approved",
		SumRUB:     cpa.RUBPtr(10000),
	})
	switch {
	case errors.Is(err, cpa.ErrWindowExpired):
		log.Fatal("окно атрибуции истекло — задним числом конверсия не засчитывается")
	case err != nil:
		// ⚠ В тексте ошибки — код и request_id: по нему находится вся цепочка на
		// стороне Recca.
		log.Fatalf("конверсия не принята: %v", err)
	}

	// И 201, и 200 — успех: второе значит «такая конверсия уже была».
	fmt.Printf("принято: %s, статус %s, выплата %s ₽\n", conversion.ExternalID, conversion.Status, conversion.PayoutRUB)
	fmt.Printf("HTTP %d, request_id %s\n", response.StatusCode, response.RequestID)
}
