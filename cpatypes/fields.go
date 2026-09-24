package cpatypes

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// ErrIncompleteData — в успешном ответе нет полного набора полей: data
// отсутствует, равен null, не содержит обязательного поля или прислал вместо
// него null.
//
// ⚠⚠ Отдельная ошибка, потому что «ноль» и «сервер не прислал данные» — разные
// ответы, и выдавать второй за первый значит показать потребителю выдуманные
// суммы: недостающие поля json.Unmarshal молча оставил бы нулями, и в
// интерфейсе появилось бы «выплата 0 ₽» вместо отказа.
var ErrIncompleteData = errors.New("cpa: в ответе нет полного набора данных")

// requiredTag — тег, которым поле объявляется обязательным по контракту.
//
// ⚠ Обязательны НЕ все поля подряд: `sub_id`, `occurred_at`, `hold_until` и
// `agent_leg` законно приходят nullʼом. Поэтому обязательность объявляется у
// самого поля — одной копией, рядом с ним, — а не списком в вызывающем коде,
// который отстал бы от типа молча.
const requiredTag = "required"

// RequireFields отвечает, содержит ли data все обязательные поля типа T и ни
// одно из них не равно null. Имена берутся из json-тегов, обязательность — из
// тега `cpa:"required"`, включая поля встроенных структур.
//
// Нулевые значения проверка пропускает: ноль — законный ответ. Посторонние поля
// рядом не мешают: новое поле в ответе — совместимая правка контракта.
//
// ⚠⚠ Тип без ни одного обязательного поля — ошибка ВЫЗОВА, а не ответа:
// проверка, которой нечего проверять, пропустила бы любые данные и выглядела бы
// при этом строгим разбором. Такое молчание опаснее отсутствия проверки.
func RequireFields[T any](data []byte) error {
	required, err := requiredNames[T]("RequireFields")
	if err != nil {
		return err
	}
	return requireIn(required, data)
}

// RequireFieldsEach — та же проверка для СТРАНИЦЫ: data обязана быть массивом, и
// полный набор полей обязана нести каждая его строка.
//
// ⚠⚠ Проверяется КАЖДАЯ строка, а не первая. «Первая полная, третья усечённая» —
// ровно тот отказ, который доезжает до интерфейса молча, и выборочная проверка
// его не видит: она зелена по той же причине, по которой зелен пустой тест.
//
// Пустой массив законен — страница без строк это ответ. А вот null и отсутствие
// data законными не являются: «строк нет» и «данных не прислали» разные вещи, и
// вторая означает, что дверь не выполнила обещание.
func RequireFieldsEach[T any](data []byte) error {
	required, err := requiredNames[T]("RequireFieldsEach")
	if err != nil {
		return err
	}
	present, err := presentData(data)
	if err != nil {
		return err
	}

	var rows []json.RawMessage
	if err := json.Unmarshal(present, &rows); err != nil {
		return fmt.Errorf("cpa: разбор data: %w", err)
	}
	for i, row := range rows {
		if err := requireIn(required, row); err != nil {
			return fmt.Errorf("строка %d: %w", i, err)
		}
	}
	return nil
}

// requiredNames — имена обязательных полей типа T; пустой набор объявляется
// ошибкой вызова, и это решение принимается ЗДЕСЬ, одной копией на обе формы
// проверки.
func requiredNames[T any](caller string) ([]string, error) {
	t := reflect.TypeFor[T]()
	required := requiredFieldNames(t)
	if len(required) == 0 {
		return nil, fmt.Errorf(
			"cpa: %s[%s]: у типа нет полей с тегом cpa:%q — проверка была бы пустой",
			caller, typeName(t), requiredTag,
		)
	}
	return required, nil
}

// presentData отличает «данных не прислали» от любого их содержимого. Отдельная
// функция, потому что этот ответ один и тот же у объекта и у страницы, а
// разойтись двум копиям было бы нечем помешать.
func presentData(data []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, fmt.Errorf("%w: data пуст", ErrIncompleteData)
	}
	return trimmed, nil
}

// requireIn — сама проверка по готовому набору имён.
func requireIn(required []string, data []byte) error {
	present, err := presentData(data)
	if err != nil {
		return err
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(present, &fields); err != nil {
		return fmt.Errorf("cpa: разбор data: %w", err)
	}

	for _, name := range required {
		raw, ok := fields[name]
		if !ok {
			return fmt.Errorf("%w: нет поля %s", ErrIncompleteData, name)
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("%w: поле %s пришло null", ErrIncompleteData, name)
		}
	}
	return nil
}

// requiredFieldNames собирает имена обязательных полей, спускаясь во встроенные
// структуры: ConversionDetail встраивает Conversion, и набор у него общий.
func requiredFieldNames(t reflect.Type) []string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}

	var names []string
	for i := range t.NumField() {
		field := t.Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")

		if field.Anonymous && name == "" {
			names = append(names, requiredFieldNames(field.Type)...)
			continue
		}
		if name == "" || name == "-" {
			continue
		}
		if !hasRequiredTag(field.Tag.Get("cpa")) {
			continue
		}
		names = append(names, name)
	}
	return names
}

func hasRequiredTag(tag string) bool {
	for _, part := range strings.Split(tag, ",") {
		if strings.TrimSpace(part) == requiredTag {
			return true
		}
	}
	return false
}

func typeName(t reflect.Type) string {
	if t.Name() != "" {
		return t.Name()
	}
	return t.String()
}
