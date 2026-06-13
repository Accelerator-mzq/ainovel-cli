package tui

import (
	"testing"

	"github.com/Accelerator-mzq/ainovel-cli/internal/host"
)

func TestSimulationCommandsAreRegisteredAndNeedIdle(t *testing.T) {
	registry := commandRegistryInstance()
	for _, name := range []string{"simulate", "importsim", "fetchsim"} {
		spec, ok := registry.Find(name)
		if !ok {
			t.Fatalf("expected /%s command to be registered", name)
		}
		if !spec.NeedsIdle {
			t.Fatalf("/%s should require idle state", name)
		}
	}

	items := builtinCommandItems()
	if !hasPaletteItem(items, "simulate") || !hasPaletteItem(items, "importsim") || !hasPaletteItem(items, "fetchsim") {
		t.Fatalf("expected simulate commands in palette: %+v", items)
	}
}

func TestSimulationCommandsAreBlockedWhileRunning(t *testing.T) {
	for _, name := range []string{"simulate", "fetchsim"} {
		m := Model{snapshot: host.UISnapshot{IsRunning: true}, eventIndex: map[string]int{}}
		next, _ := m.handleSlashCommand(slashCommand{name: name})
		got := next.(Model)
		if len(got.events) != 1 || got.events[0].Category != "ERROR" {
			t.Fatalf("/%s: expected NeedsIdle to emit one error, got %+v", name, got.events)
		}
		if got.simulator != nil {
			t.Fatalf("/%s: modal should not start while runtime is running", name)
		}
	}
}

func hasPaletteItem(items []commandPaletteItem, name string) bool {
	for _, item := range items {
		if item.Name == name {
			return true
		}
	}
	return false
}
