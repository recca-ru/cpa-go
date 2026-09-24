package cpa

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"go.recca.ru/cpa/cpatypes"
	"go.recca.ru/cpa/internal/transport"
)

// service — общая часть всех сервисов клиента. Каждый сервис ЕСТЬ этот же тип
// под своим именем (`type ConversionsService service`), а поля клиента —
// указатели на одну и ту же структуру.
//
// ⚠ Форма выглядит непривычно и взята она не ради экономии аллокаций (их тут
// восемь на клиента, то есть нисколько). Смысл в другом: «что сервис знает о
// клиенте» объявлено ОДИН раз. Заведи каждый сервис собственной структурой — и
// девятый спокойно возьмёт вторую зависимость, разойдясь с остальными молча.
type service struct {
	client *Client
}

// request — одно обращение сервиса к двери. Внутренний тип: наружу методы
// отдают типизированные параметры, а не эту структуру.
type request struct {
	Method string
	// Path — путь ОТ версии двери: "/cpa/conversions", а не
	// "/federation/v1/cpa/conversions".
	Path  string
	Query url.Values
	Body  any

	// Subject — предмет операции словом и в именительном падеже («конверсия»,
	// «страница конверсий»). Уходит в текст ошибки разбора рядом с request_id:
	// без него интегратору нечего назвать в обращении, а нам нечего искать в
	// журналах.
	Subject string

	// IdempotentWrite объявляет, что повтор этой ЗАПИСИ безопасен: у операции
	// есть ключ идемпотентности, и вторая попытка вернёт первый результат, а не
	// заплатит второй раз.
	//
	// ⚠⚠ Умолчание «не повторять» несимметрично намеренно. Забытое поле стоит
	// потерянных повторов — операция просто не переживёт перезапуск бэкенда;
	// поставленное по ошибке стоит второй конверсии. Терять повторы дешевле.
	//
	// ⚠ Проверить это за вызывающего нечем: идемпотентность — свойство ДВЕРИ, а
	// не формы запроса. Clicks.Create выглядит ровно как Conversions.Create, а
	// повторяться не должен: повтор там есть второй переход.
	//
	// Чтения (GET, HEAD) повторяются всегда и этого поля не требуют — забыть его
	// у чтения нельзя.
	IdempotentWrite bool
}

// do исполняет запрос сервиса и разбирает data ответа в T.
//
// Три решения принимаются здесь ОДИН раз, а не в каждом из восемнадцати методов
// контракта: повторять ли запрос, полон ли ответ и что написано в ошибке
// разбора. Ровно эти три и оказались местом дефектов в пробе WV-0698 — методы
// же между собой отличаются только путём и типом.
//
// ⚠ Дженерик свободной функцией, а не методом: методы с параметром типа Go не
// поддерживает.
func do[T any](ctx context.Context, s *service, r request) (*T, *Response, error) {
	var data json.RawMessage
	resp, err := s.client.exec.Do(ctx, r.transport(&data))
	if err != nil {
		return nil, resp, err
	}

	// ⚠ Строгая проверка ДО заполнения структуры: недостающее поле
	// json.Unmarshal оставил бы нулём, и наружу ушла бы сумма, которой дверь не
	// присылала.
	if err := cpatypes.RequireFields[T](data); err != nil {
		return nil, resp, r.wrap(resp, err)
	}

	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, resp, r.wrap(resp, fmt.Errorf("разбор data: %w", err))
	}
	return &value, resp, nil
}

// doList — то же для страницы: data приезжает массивом, и полный набор полей
// обязана нести каждая строка, а не первая.
//
// Пагинация достаётся из Response.Meta — у страницы это часть ответа, а не
// данных.
func doList[T any](ctx context.Context, s *service, r request) ([]T, *Response, error) {
	var data json.RawMessage
	resp, err := s.client.exec.Do(ctx, r.transport(&data))
	if err != nil {
		return nil, resp, err
	}

	if err := cpatypes.RequireFieldsEach[T](data); err != nil {
		return nil, resp, r.wrap(resp, err)
	}

	var page []T
	if err := json.Unmarshal(data, &page); err != nil {
		return nil, resp, r.wrap(resp, fmt.Errorf("разбор data: %w", err))
	}
	return page, resp, nil
}

// transport собирает запрос исполнителю и принимает ЕДИНСТВЕННОЕ решение о
// повторе: чтение повторяется всегда, запись — только когда операция объявила
// себя идемпотентной.
func (r request) transport(out any) transport.Request {
	return transport.Request{
		Method:    r.Method,
		Path:      r.Path,
		Query:     r.Query,
		Body:      r.Body,
		Out:       out,
		Retryable: isReadMethod(r.Method) || r.IdempotentWrite,
	}
}

// pathSegment готовит значение к подстановке в путь.
//
// ⚠⚠ Экранирование обязательно, и не из осторожности: external_id конверсии —
// строка до 200 знаков БЕЗ ограничения алфавита (в отличие от идентификатора
// партнёра), то есть в ней законно бывают слеш, вопрос и решётка. Подставь её
// как есть — и «order/17?x=1» превратится в другой путь с параметрами, а дверь
// ответит про другую конверсию либо 404. Транспорт склеивает путь как есть и за
// вызывающего этого не делает.
func pathSegment(value string) string { return url.PathEscape(value) }

// validationError — отказ до сети из пакета cpa: форма та же, что у проверок
// cpatypes, чтобы errors.Is(err, ErrValidation) работал одинаково с любой стороны
// границы.
func validationError(field, reason string) error {
	return &ValidationError{Field: field, Reason: reason}
}

// wrap привязывает к ошибке разбора предмет операции и request_id.
func (r request) wrap(resp *Response, err error) error {
	subject := r.Subject
	if subject == "" {
		// Забытый предмет сообщение не обесценивает: путь называет операцию не
		// так удобно, но однозначно. Пустое место на его месте было бы хуже.
		subject = r.Path
	}
	return fmt.Errorf("cpa: %s (request_id %s): %w", subject, resp.RequestID, err)
}
