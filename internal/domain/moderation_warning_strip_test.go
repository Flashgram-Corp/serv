package domain

import (
	"strings"
	"testing"
)

func TestStripProfileWarningRemovesInjectedLine(t *testing.T) {
	resetModerationProfileWarning(t)

	warning := ModerationProfileWarning(true, false, "я андрей вот мое описание")
	if got := StripProfileWarning(warning); got != "я андрей вот мое описание" {
		t.Fatalf("stripped = %q, want the owner's text only", got)
	}
	if got := StripProfileWarning(ModerationProfileWarning(false, true, "")); got != "" {
		t.Fatalf("stripped empty-about = %q, want empty", got)
	}
}

// Клиент возвращает в account.updateProfile то, что видел в profile, поэтому
// предупреждение возвращается тем же текстом -- после починки правки описания
// старые дубли из базы должны вычищаться за один проход.
func TestStripProfileWarningHealsRepeatedDuplicates(t *testing.T) {
	resetModerationProfileWarning(t)

	warning := defaultScamProfileWarning
	duplicated := strings.Join([]string{warning, warning, warning, "моё описание"}, "\n")
	if got := StripProfileWarning(duplicated); got != "моё описание" {
		t.Fatalf("stripped = %q, want the owner's text once", got)
	}
	// Текст без последнего перевода строки (предупреждение не в начале) не трогаем.
	if got := StripProfileWarning("моё описание\n" + warning); got != "моё описание\n"+warning {
		t.Fatalf("stripped mid-text = %q, want it untouched", got)
	}
}

// operator может поменять TELESRV_SCAM_WARNING, тогда в базе остаются строки
// прежней редакции -- их тоже надо уметь вычистить.
func TestStripProfileWarningAlsoRemovesPreviousWording(t *testing.T) {
	resetModerationProfileWarning(t)

	stored := defaultScamProfileWarning + "\nстарое описание"
	SetModerationProfileWarning("⚠️ Аккаунт помечен как SCAM.", "⚠️ Аккаунт помечен как FAKE.")
	if got := StripProfileWarning(stored); got != "старое описание" {
		t.Fatalf("stripped = %q, want the previous wording removed too", got)
	}
	// Новая формулировка, конечно, тоже срезается.
	if got := StripProfileWarning("⚠️ Аккаунт помечен как SCAM.\nновое описание"); got != "новое описание" {
		t.Fatalf("stripped current wording = %q", got)
	}
}

func TestStripProfileWarningKeepsOwnerText(t *testing.T) {
	resetModerationProfileWarning(t)

	for _, about := range []string{
		"",
		"обычное описание",
		"я упоминаю слово SCAM в своём описании",
		"line\nSCAM",
		strings.TrimSuffix(defaultScamProfileWarning, ".") + "но это мой собственный текст",
	} {
		if got := StripProfileWarning(about); got != about {
			t.Errorf("StripProfileWarning(%q) = %q, want it untouched", about, got)
		}
	}
}