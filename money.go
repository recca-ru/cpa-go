package cpa

import "go.recca.ru/cpa/cpatypes"

// Деньги, идентификаторы и величины контракта под короткими именами. Сущности
// контракта и их параметры — в types.go: один дом на один род имён, иначе через
// волну они разойдутся по файлам случайным образом.
type (
	// RUB — сумма в рублях целым числом. Дробная сумма едет лексической
	// формой — см. CreateConversionParams.SumRUBExact.
	RUB = cpatypes.RUB
	// ClickID — идентификатор перехода, выданный POST /cpa/clicks.
	ClickID = cpatypes.ClickID
	// PartnerExternalID — идентификатор партнёра, назначенный интегратором.
	PartnerExternalID = cpatypes.PartnerExternalID
)

// Величины контракта, которые нужны вызывающему коду до отправки запроса.
const (
	MaxSumRUB                = cpatypes.MaxSumRUB
	MaxExternalIDLen         = cpatypes.MaxExternalIDLen
	MaxSubIDLen              = cpatypes.MaxSubIDLen
	MaxGoalLen               = cpatypes.MaxGoalLen
	MaxPartnerExternalIDLen  = cpatypes.MaxPartnerExternalIDLen
	ReservedExternalIDPrefix = cpatypes.ReservedExternalIDPrefix
)

// IsReservedExternalID отвечает, принадлежит ли номер заказа внутреннему
// пространству платформы — по тому же правилу, что дверь: без учёта регистра и
// пробелов впереди.
func IsReservedExternalID(id string) bool { return cpatypes.IsReservedExternalID(id) }

// Ptr возвращает указатель на значение — для полей, у которых «не передали» и
// «передали пустым» означают разное (раздел 7 контракта).
func Ptr[T any](v T) *T { return cpatypes.Ptr(v) }

// RUBPtr — то же самое для денег, отдельным именем ради читаемости вызова:
// SumRUB: cpa.RUBPtr(10000) говорит о сумме, cpa.Ptr(cpa.RUB(10000)) — о
// языке.
func RUBPtr(v RUB) *RUB { return cpatypes.Ptr(v) }

// RequireFields отвечает, содержит ли data все поля типа T и ни одно из них не
// равно null. Нужен методам, разбирающим ответ: недостающее поле json.Unmarshal
// оставил бы нулём, и наружу ушли бы суммы, которых дверь не присылала.
func RequireFields[T any](data []byte) error { return cpatypes.RequireFields[T](data) }

// RequireFieldsEach — то же для страницы: data обязана быть массивом, и полный
// набор полей обязана нести КАЖДАЯ строка. Нужен тому, кто разбирает список сам
// через Client.Do.
func RequireFieldsEach[T any](data []byte) error { return cpatypes.RequireFieldsEach[T](data) }
