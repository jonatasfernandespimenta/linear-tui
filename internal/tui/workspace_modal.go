package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/roeyazroel/linear-tui/internal/auth"
)

// workspaceSwitcherPage is the pages name of the workspace switcher overlay.
const workspaceSwitcherPage = "workspace_switcher"

// WorkspaceSwitcherModal lists connected workspaces and switches between them.
type WorkspaceSwitcherModal struct {
	app       *App
	modal     *tview.Flex
	list      *tview.List
	titleView *tview.TextView
	helpView  *tview.TextView
	profiles  []auth.WorkspaceProfile
	// connectIndex is the list row that starts the connect-workspace flow.
	connectIndex int
}

// NewWorkspaceSwitcherModal creates the workspace switcher overlay.
func NewWorkspaceSwitcherModal(app *App) *WorkspaceSwitcherModal {
	wm := &WorkspaceSwitcherModal{app: app}

	wm.list = tview.NewList().
		ShowSecondaryText(false).
		SetMainTextColor(app.theme.Foreground).
		SetSelectedBackgroundColor(app.theme.Accent).
		SetSelectedTextColor(app.theme.SelectionText).
		SetHighlightFullLine(true)
	wm.list.SetBackgroundColor(app.theme.HeaderBg)

	wm.titleView = tview.NewTextView()
	wm.titleView.SetDynamicColors(true)
	wm.titleView.SetTextColor(app.theme.Accent)
	wm.titleView.SetBackgroundColor(app.theme.HeaderBg)

	wm.helpView = tview.NewTextView()
	wm.helpView.SetDynamicColors(true)
	wm.helpView.SetTextColor(app.theme.SecondaryText)
	wm.helpView.SetBackgroundColor(app.theme.HeaderBg)

	modalContent := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(wm.titleView, 1, 0, false).
		AddItem(wm.list, 0, 1, true).
		AddItem(wm.helpView, 1, 0, false)
	modalContent.Box = tview.NewBox().SetBackgroundColor(app.theme.HeaderBg)
	modalContent.SetBackgroundColor(app.theme.HeaderBg).
		SetBorder(true).
		SetBorderColor(app.theme.Accent).
		SetTitle(" Switch Workspace ").
		SetTitleColor(app.theme.Foreground)
	padding := app.density.ModalPadding
	modalContent.SetBorderPadding(padding.Top, padding.Bottom, padding.Left, padding.Right)

	wm.modal = tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().
			SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(modalContent, 15, 0, true).
			AddItem(nil, 0, 1, false), 54, 0, true).
		AddItem(nil, 0, 1, false)
	wm.modal.SetBackgroundColor(app.theme.Background)

	return wm
}

// Show displays the workspace switcher with the currently saved workspaces.
func (wm *WorkspaceSwitcherModal) Show() {
	wm.app.reloadWorkspaceProfiles()
	wm.profiles = wm.app.WorkspaceProfiles()
	activeID := wm.app.ActiveWorkspaceID()
	envOverride := wm.app.WorkspaceEnvOverride()

	wm.list.Clear()
	current := 0
	for i, profile := range wm.profiles {
		marker := ""
		if profile.WorkspaceID == activeID {
			marker = "●"
			if envOverride {
				marker = "● ENV"
			}
			current = i
		}
		wm.list.AddItem(formatWorkspaceRow(profile.DisplayName(), marker), "", 0, nil)
	}

	wm.connectIndex = wm.list.GetItemCount()
	wm.list.AddItem("+ Connect another workspace", "", 0, nil)
	wm.list.SetCurrentItem(current)

	switch {
	case envOverride:
		wm.titleView.SetText(fmt.Sprintf("%sLINEAR_API_KEY overrides saved credentials[-]", wm.app.themeTags.Warning))
	case len(wm.profiles) == 0:
		wm.titleView.SetText(fmt.Sprintf("%sNo workspaces connected[-]", wm.app.themeTags.SecondaryText))
	default:
		wm.titleView.SetText(fmt.Sprintf("%sActive: %s[-]", wm.app.themeTags.Accent, wm.app.ActiveWorkspaceName()))
	}
	wm.helpView.SetText("Enter switch | n connect | d disconnect | Esc cancel")

	wm.app.pages.AddPage(workspaceSwitcherPage, wm.modal, true, true)
	wm.app.pages.SendToFront(workspaceSwitcherPage)
	wm.app.app.SetFocus(wm.list)
}

// Hide closes the workspace switcher.
func (wm *WorkspaceSwitcherModal) Hide() {
	wm.app.pages.RemovePage(workspaceSwitcherPage)
	wm.app.updateFocus()
}

// HandleKey handles keyboard input for the workspace switcher.
func (wm *WorkspaceSwitcherModal) HandleKey(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyEscape:
		wm.Hide()
		return nil
	case tcell.KeyEnter:
		wm.activateCurrent()
		return nil
	case tcell.KeyUp:
		wm.move(-1)
		return nil
	case tcell.KeyDown:
		wm.move(1)
		return nil
	case tcell.KeyRune:
		switch event.Rune() {
		case 'j':
			wm.move(1)
			return nil
		case 'k':
			wm.move(-1)
			return nil
		case 'n':
			wm.Hide()
			wm.app.ConnectWorkspace()
			return nil
		case 'd':
			wm.disconnectCurrent()
			return nil
		}
	}
	return event
}

// activateCurrent switches to the selected workspace or starts the connect flow.
func (wm *WorkspaceSwitcherModal) activateCurrent() {
	index := wm.list.GetCurrentItem()
	wm.Hide()
	if index == wm.connectIndex {
		wm.app.ConnectWorkspace()
		return
	}
	if index < 0 || index >= len(wm.profiles) {
		return
	}
	wm.app.SwitchWorkspace(wm.profiles[index].WorkspaceID)
}

// disconnectCurrent removes the selected workspace after confirmation.
func (wm *WorkspaceSwitcherModal) disconnectCurrent() {
	index := wm.list.GetCurrentItem()
	if index < 0 || index >= len(wm.profiles) {
		return
	}
	workspaceID := wm.profiles[index].WorkspaceID
	wm.Hide()
	wm.app.DisconnectWorkspace(workspaceID)
}

// move changes the list selection by delta, clamped to the list bounds.
func (wm *WorkspaceSwitcherModal) move(delta int) {
	index := wm.list.GetCurrentItem() + delta
	if index < 0 || index >= wm.list.GetItemCount() {
		return
	}
	wm.list.SetCurrentItem(index)
}

// formatWorkspaceRow renders a workspace row with its right-aligned marker.
func formatWorkspaceRow(name, marker string) string {
	if marker == "" {
		return name
	}
	const width = 34
	padding := width - len([]rune(name))
	if padding < 1 {
		padding = 1
	}
	return fmt.Sprintf("%s%*s", name, padding+len([]rune(marker)), marker)
}

// GetModal returns the modal flex for adding to pages.
func (wm *WorkspaceSwitcherModal) GetModal() *tview.Flex {
	return wm.modal
}
