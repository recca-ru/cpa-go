package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// journalName — файл журнала внутри .git/.
const journalName = "mutate-journal.json"

// Mutation — одна правка исходника и ожидание от тестов.
type Mutation struct {
	Name string
	// File — путь от корня репозитория.
	File string
	// Old заменяется на New ровно в одном месте. При Create файла ещё нет, и New —
	// его содержимое целиком.
	Old, New string
	Create   bool
	// Pattern и Pkg — что гонять: `go test -run Pattern Pkg`.
	Pattern, Pkg string
	// WantRed — ждём падения. false — разрешённый случай: проверка обязана его
	// пропустить, иначе она требует худшего кода.
	WantRed bool
}

// record — запись журнала. Existed=false значит «файла до мутации не было»:
// восстановить — значит удалить.
type record struct {
	Path     string `json:"path"`
	Existed  bool   `json:"existed"`
	Original []byte `json:"original"`
	Mutated  []byte `json:"mutated"`
}

// applied — мутация, стоящая в дереве прямо сейчас.
type applied struct {
	path string
	rec  record
}

// apply пишет журнал, затем мутацию. Порядок обязателен: убей процесс между
// двумя записями — журнал уже знает, что возвращать, а файл ещё не тронут.
func apply(root, journal string, m Mutation) (*applied, error) {
	path := filepath.Join(root, filepath.FromSlash(m.File))
	rec := record{Path: m.File}

	if m.Create {
		if _, err := os.Stat(path); err == nil {
			return nil, fmt.Errorf("файл-зонд %s уже существует", m.File)
		}
		rec.Mutated = []byte(m.New)
	} else {
		original, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if n := strings.Count(string(original), m.Old); n != 1 {
			return nil, fmt.Errorf("якорь встречается %d раз — мутация НЕ применена", n)
		}
		rec.Existed = true
		rec.Original = original
		rec.Mutated = []byte(strings.Replace(string(original), m.Old, m.New, 1))
	}

	if err := writeJournal(journal, rec); err != nil {
		return nil, fmt.Errorf("журнал не записан, мутацию не применяю: %w", err)
	}
	if err := os.WriteFile(path, rec.Mutated, 0o644); err != nil {
		_ = os.Remove(journal)
		return nil, err
	}
	return &applied{path: path, rec: rec}, nil
}

// revert возвращает файл к исходному.
func (a *applied) revert() error {
	if !a.rec.Existed {
		err := os.Remove(a.path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return os.WriteFile(a.path, a.rec.Original, 0o644)
}

func writeJournal(journal string, rec record) error {
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.Create(journal)
	if err != nil {
		return err
	}
	if _, err := f.Write(raw); err != nil {
		f.Close()
		return err
	}
	// Sync — потому что журнал нужен ровно в тот момент, когда процесс умер:
	// запись, оставшаяся в буфере, спасает только тех, кому она не нужна.
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// recoverJournal возвращает файл, оставленный прерванным прогоном. Возвращает
// путь восстановленного файла или "" — если журнала нет.
//
// ⚠ Восстанавливает ТОЛЬКО если файл сейчас ровно такой, каким его оставила
// мутация (или мутация не успела записаться). Файл, изменённый после обрыва,
// — уже чья-то работа: затереть её исходником значило бы потерять её молча.
// Тогда стоп, и решает человек.
func recoverJournal(root, journal string) (string, error) {
	raw, err := os.ReadFile(journal)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var rec record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return "", fmt.Errorf("журнал %s не разбирается: %w — разберите руками и удалите", journal, err)
	}
	path := filepath.Join(root, filepath.FromSlash(rec.Path))
	current, readErr := os.ReadFile(path)
	exists := readErr == nil
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return "", readErr
	}

	switch {
	case exists && bytes.Equal(current, rec.Mutated):
		// Мутация стоит в дереве — возвращаем.
		a := &applied{path: path, rec: rec}
		if err := a.revert(); err != nil {
			return "", err
		}
	case rec.Existed && exists && bytes.Equal(current, rec.Original):
		// Процесс умер между журналом и мутацией либо уже после отката.
	case !rec.Existed && !exists:
		// Зонд не успел появиться или уже удалён.
	default:
		return "", fmt.Errorf("%s изменён после прерванного прогона — не трогаю; журнал %s, исходник внутри", rec.Path, journal)
	}
	if err := os.Remove(journal); err != nil {
		return "", err
	}
	return rec.Path, nil
}
