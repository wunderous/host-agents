package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/wunderous/host-agents/internal/tools"
)

func TestBubbleModelRendersOutputAndQuitsFromExitCommand(t *testing.T) {
	input := newInteractiveIO(context.Background())
	defer input.Close()
	app := &App{catalog: NewCatalog(tools.CapabilityCatalogSnapshot{
		ProviderID: "incus",
		Revision:   "test-revision",
		Tools:      []tools.CapabilityDescriptor{{Name: "get_host_info"}},
	})}
	model := newBubbleModel(context.Background(), app, input)
	model.resize()

	updated, _ := model.Update(bubbleEvent{kind: bubbleOutputEvent, text: "host is ready"})
	model = updated.(bubbleModel)
	if !strings.Contains(model.View(), "host is ready") {
		t.Fatalf("Bubble Tea view omitted output: %s", model.View())
	}

	model.commandInput.SetValue("/exit")
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.(bubbleModel).executing {
		t.Fatal("exit command entered execution state")
	}
	if command == nil {
		t.Fatal("exit command did not return a quit command")
	}
	if _, ok := command().(tea.QuitMsg); !ok {
		t.Fatalf("exit command returned %T, want tea.QuitMsg", command())
	}
}

func TestBubbleModelRoutesPromptAnswersToCommandReader(t *testing.T) {
	input := newInteractiveIO(context.Background())
	defer input.Close()
	app := &App{catalog: NewCatalog(tools.CapabilityCatalogSnapshot{
		ProviderID: "incus",
		Revision:   "test-revision",
		Tools:      []tools.CapabilityDescriptor{{Name: "get_host_info"}},
	})}
	model := newBubbleModel(context.Background(), app, input)
	model.executing = true
	model.prompt = "approve [y/N]> "
	model.commandInput.SetValue("yes")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(bubbleModel)
	if model.prompt != "" {
		t.Fatalf("prompt remained visible after answer: %q", model.prompt)
	}
	select {
	case answer := <-input.answers:
		if answer != "yes" {
			t.Fatalf("answer = %q, want yes", answer)
		}
	default:
		t.Fatal("Bubble Tea did not route the prompt answer")
	}
}
