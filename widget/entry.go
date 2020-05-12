package widget

import (
	"image/color"
	"math"
	"strings"
	"unicode"

	"fyne.io/fyne"
	"fyne.io/fyne/canvas"
	"fyne.io/fyne/driver/desktop"
	"fyne.io/fyne/internal/widget"
	"fyne.io/fyne/theme"
)

const (
	multiLineRows            = 3
	doubleClickWordSeperator = "`~!@#$%^&*()-=+[{]}\\|;:'\",.<>/?"
)

// Declare conformity with interfaces
var _ fyne.Disableable = (*Entry)(nil)
var _ fyne.Draggable = (*Entry)(nil)
var _ fyne.Focusable = (*Entry)(nil)
var _ fyne.Tappable = (*Entry)(nil)
var _ fyne.Widget = (*Entry)(nil)
var _ desktop.Mouseable = (*Entry)(nil)
var _ desktop.Keyable = (*Entry)(nil)

// Entry widget allows simple text to be input when focused.
type Entry struct {
	DisableableWidget
	shortcut    fyne.ShortcutHandler
	Text        string
	PlaceHolder string
	OnChanged   func(string) `json:"-"`
	Password    bool
	ReadOnly    bool // Deprecated: Use Disable() instead
	MultiLine   bool
	Wrapping    fyne.TextWrap

	CursorRow, CursorColumn int
	OnCursorChanged         func() `json:"-"`

	focused     bool
	text        *textProvider
	placeholder *textProvider

	// selectRow and selectColumn represent the selection start location
	// The selection will span from selectRow/Column to CursorRow/Column -- note that the cursor
	// position may occur before or after the select start position in the text.
	selectRow, selectColumn int

	// selectKeyDown indicates whether left shift or right shift is currently held down
	selectKeyDown bool

	// selecting indicates whether the cursor has moved since it was at the selection start location
	selecting bool
	popUp     *widget.PopUpMenu
	// TODO: Add OnSelectChanged

	// ActionItem is a small item which is displayed at the outer right of the entry (like a password revealer)
	ActionItem fyne.CanvasObject
}

// NewEntry creates a new single line entry widget.
func NewEntry() *Entry {
	e := &Entry{}
	e.ExtendBaseWidget(e)
	return e
}

// NewMultiLineEntry creates a new entry that allows multiple lines
func NewMultiLineEntry() *Entry {
	e := &Entry{MultiLine: true}
	e.ExtendBaseWidget(e)
	return e
}

// NewPasswordEntry creates a new entry password widget
func NewPasswordEntry() *Entry {
	e := &Entry{Password: true}
	e.ExtendBaseWidget(e)
	e.ActionItem = newPasswordRevealer(e)
	return e
}

// CreateRenderer is a private method to Fyne which links this widget to its renderer
func (e *Entry) CreateRenderer() fyne.WidgetRenderer {
	e.ExtendBaseWidget(e)

	line := canvas.NewRectangle(theme.ButtonColor())
	cursor := canvas.NewRectangle(theme.FocusColor())
	cursor.Hide()

	var objects []fyne.CanvasObject
	e.SetFields(func() {
		objects = []fyne.CanvasObject{line, e.placeholderProvider(), e.textProvider(), cursor}

		if e.Password && e.ActionItem == nil {
			// An entry widget has been created via struct setting manually
			// the Password field to true. Going to enable the password revealer.
			e.ActionItem = newPasswordRevealer(e)
		}

		if e.ActionItem != nil {
			objects = append(objects, e.ActionItem)
		}
	})
	return &entryRenderer{line, cursor, []fyne.CanvasObject{}, objects, e}
}

// Cursor returns the cursor type of this widget
func (e *Entry) Cursor() desktop.Cursor {
	return desktop.TextCursor
}

// Disable this widget so that it cannot be interacted with, updating any style appropriately.
func (e *Entry) Disable() { // TODO remove this override after ReadOnly is removed
	e.ReadOnly = true

	e.DisableableWidget.Disable()
}

// Disabled satisfies the fyne.Disableable interface.
func (e *Entry) Disabled() bool {
	return e.DisableableWidget.disabled || e.ReadOnly
}

// DoubleTapped is called when this entry has been double tapped so we should select text below the pointer
func (e *Entry) DoubleTapped(_ *fyne.PointEvent) {
	row := e.textProvider().row(e.CursorRow)

	start, end := getTextWhitespaceRegion(row, e.CursorColumn)
	if start == -1 || end == -1 {
		return
	}

	e.SetFieldsAndRefresh(func() {
		if e.selectKeyDown == false {
			e.selectRow = e.CursorRow
			e.selectColumn = start
		}
		// Always aim to maximise the selected region
		if e.selectRow > e.CursorRow || (e.selectRow == e.CursorRow && e.selectColumn > e.CursorColumn) {
			e.CursorColumn = start
		} else {
			e.CursorColumn = end
		}
		e.selecting = true
	})
}

// DragEnd is called at end of a drag event - currently ignored
func (e *Entry) DragEnd() {
}

// Dragged is called when the pointer moves while a button is held down
func (e *Entry) Dragged(d *fyne.DragEvent) {
	e.selecting = true
	e.updateMousePointer(&d.PointEvent, false)
}

// Enable this widget, updating any style or features appropriately.
func (e *Entry) Enable() { // TODO remove this override after ReadOnly is removed
	e.ReadOnly = false

	e.DisableableWidget.Enable()
}

// ExtendBaseWidget is used by an extending widget to make use of BaseWidget functionality.
func (e *Entry) ExtendBaseWidget(wid fyne.Widget) {
	e.SetFields(func() {
		if e.BaseWidget.impl != nil {
			return
		}

		e.BaseWidget.impl = wid
	})

	e.registerShortcut()
}

// Focused returns whether or not this Entry has focus.
// Deprecated: this method will be removed as it is no longer required, widgets do not expose their focus state.
func (e *Entry) Focused() bool {
	return e.GetField(func() interface{} {
		return e.focused
	}).(bool)
}

// FocusGained is called when the Entry has been given focus.
func (e *Entry) FocusGained() {
	if e.Disabled() {
		return
	}
	e.SetFieldsAndRefresh(func() {
		e.focused = true
	})
}

// FocusLost is called when the Entry has had focus removed.
func (e *Entry) FocusLost() {
	e.SetFieldsAndRefresh(func() {
		e.focused = false
	})

}

// Hide satisfies the fyne.CanvasObject interface.
func (e *Entry) Hide() {
	if e.popUp != nil {
		e.popUp.Hide()
		e.popUp = nil
	}
	e.DisableableWidget.Hide()
}

// KeyDown handler for keypress events - used to store shift modifier state for text selection
func (e *Entry) KeyDown(key *fyne.KeyEvent) {
	// For keyboard cursor controlled selection we now need to store shift key state and selection "start"
	// Note: selection start is where the highlight started (if the user moves the selection up or left then
	// the selectRow/Column will not match SelectionStart)
	if key.Name == desktop.KeyShiftLeft || key.Name == desktop.KeyShiftRight {
		if e.selecting == false {
			e.selectRow = e.CursorRow
			e.selectColumn = e.CursorColumn
		}
		e.selectKeyDown = true
	}
}

// KeyUp handler for key release events - used to reset shift modifier state for text selection
func (e *Entry) KeyUp(key *fyne.KeyEvent) {
	// Handle shift release for keyboard selection
	// Note: if shift is released then the user may repress it without moving to adjust their old selection
	if key.Name == desktop.KeyShiftLeft || key.Name == desktop.KeyShiftRight {
		e.selectKeyDown = false
	}
}

// MinSize returns the size that this widget should not shrink below
func (e *Entry) MinSize() fyne.Size {
	e.ExtendBaseWidget(e)

	min := e.BaseWidget.MinSize()
	if e.ActionItem != nil {
		min = min.Add(fyne.NewSize(theme.IconInlineSize()+theme.Padding(), 0))
	}

	return min
}

// MouseDown called on mouse click, this triggers a mouse click which can move the cursor,
// update the existing selection (if shift is held), or start a selection dragging operation.
func (e *Entry) MouseDown(m *desktop.MouseEvent) {
	if e.selectKeyDown {
		e.selecting = true
	}
	if e.selecting && e.selectKeyDown == false && m.Button == desktop.LeftMouseButton {
		e.selecting = false
	}
	e.updateMousePointer(&m.PointEvent, m.Button == desktop.RightMouseButton)
}

// MouseUp called on mouse release
// If a mouse drag event has completed then check to see if it has resulted in an empty selection,
// if so, and if a text select key isn't held, then disable selecting
func (e *Entry) MouseUp(_ *desktop.MouseEvent) {
	start, _ := e.selection()
	if start == -1 && e.selecting && e.selectKeyDown == false {
		e.selecting = false
	}
}

// SelectedText returns the text currently selected in this Entry.
// If there is no selection it will return the empty string.
func (e *Entry) SelectedText() string {
	if e.selecting == false {
		return ""
	}

	start, stop := e.selection()
	return string(e.textProvider().buffer[start:stop])
}

// SetPlaceHolder sets the text that will be displayed if the entry is otherwise empty
func (e *Entry) SetPlaceHolder(text string) {
	e.PlaceHolder = text
	e.placeholderProvider().SetText(text) // refreshes
}

// SetReadOnly sets whether or not the Entry should not be editable
// Deprecated: Use Disable() instead.
func (e *Entry) SetReadOnly(ro bool) {
	if ro {
		e.Disable()
	} else {
		e.Enable()
	}
}

// SetText manually sets the text of the Entry to the given text value.
func (e *Entry) SetText(text string) {
	e.textProvider().SetText(text)
	e.updateText(text)

	if text == "" {
		e.SetFieldsAndRefresh(func() {
			e.CursorColumn = 0
			e.CursorRow = 0
		})
		return
	}
	provider := e.textProvider()
	e.SetFields(func() {
		if e.CursorRow >= provider.rows() {
			e.CursorRow = provider.rows() - 1
		}
		rowLength := provider.rowLength(e.CursorRow)
		if e.CursorColumn >= rowLength {
			e.CursorColumn = rowLength
		}
	})
}

// Tapped is called when this entry has been tapped so we should update the cursor position.
func (e *Entry) Tapped(ev *fyne.PointEvent) {
	e.updateMousePointer(ev, false)
}

// TappedSecondary is called when right or alternative tap is invoked.
//
// Opens the PopUpMenu with `Paste` item to paste text from the clipboard.
func (e *Entry) TappedSecondary(pe *fyne.PointEvent) {
	cutItem := fyne.NewMenuItem("Cut", func() {
		clipboard := fyne.CurrentApp().Driver().AllWindows()[0].Clipboard()
		e.cutToClipboard(clipboard)
	})
	copyItem := fyne.NewMenuItem("Copy", func() {
		clipboard := fyne.CurrentApp().Driver().AllWindows()[0].Clipboard()
		e.copyToClipboard(clipboard)
	})
	pasteItem := fyne.NewMenuItem("Paste", func() {
		clipboard := fyne.CurrentApp().Driver().AllWindows()[0].Clipboard()
		e.pasteFromClipboard(clipboard)
	})
	selectAllItem := fyne.NewMenuItem("Select all", e.selectAll)

	super := e.super()
	entryPos := fyne.CurrentApp().Driver().AbsolutePositionForObject(super)
	popUpPos := entryPos.Add(fyne.NewPos(pe.Position.X, pe.Position.Y))
	c := fyne.CurrentApp().Driver().CanvasForObject(super)

	if e.Disabled() && e.concealed() {
		return // no popup options for a disabled concealed field
	}

	var menu *fyne.Menu
	if e.Disabled() {
		menu = fyne.NewMenu("", copyItem, selectAllItem)
	} else if e.concealed() {
		menu = fyne.NewMenu("", pasteItem, selectAllItem)
	} else {
		menu = fyne.NewMenu("", cutItem, copyItem, pasteItem, selectAllItem)
	}
	e.popUp = newPopUpMenu(menu, c)
	e.popUp.ShowAtPosition(popUpPos)
}

// TypedKey receives key input events when the Entry widget is focused.
func (e *Entry) TypedKey(key *fyne.KeyEvent) {
	if e.Disabled() {
		return
	}

	provider := e.textProvider()

	if e.selectKeyDown || e.selecting {
		if e.selectingKeyHandler(key) {
			e.Refresh()
			return
		}
	}

	switch key.Name {
	case fyne.KeyBackspace:
		isEmpty := e.GetField(func() interface{} {
			return provider.len() == 0 || (e.CursorColumn == 0 && e.CursorRow == 0)
		}).(bool)
		if isEmpty {
			return
		}
		e.SetFields(func() {
			pos := e.cursorTextPos()
			provider.deleteFromTo(pos-1, pos)
			e.CursorRow, e.CursorColumn = e.rowColFromTextPos(pos - 1)
		})
	case fyne.KeyDelete:
		pos := e.cursorTextPos()
		if provider.len() == 0 || pos == provider.len() {
			return
		}
		e.SetFields(func() {
			provider.deleteFromTo(pos, pos+1)
		})
	case fyne.KeyReturn, fyne.KeyEnter:
		if e.GetField(func() interface{} {
			return !e.MultiLine
		}).(bool) {
			return
		}
		e.SetFields(func() {
			provider.insertAt(e.cursorTextPos(), []rune("\n"))
			e.CursorColumn = 0
			e.CursorRow++
		})
	case fyne.KeyUp:
		if e.GetField(func() interface{} {
			return !e.MultiLine
		}).(bool) {
			return
		}

		e.SetFields(func() {
			if e.CursorRow > 0 {
				e.CursorRow--
			}

			rowLength := provider.rowLength(e.CursorRow)
			if e.CursorColumn > rowLength {
				e.CursorColumn = rowLength
			}
		})
	case fyne.KeyDown:
		if e.GetField(func() interface{} {
			return !e.MultiLine
		}).(bool) {
			return
		}

		e.SetFields(func() {
			if e.CursorRow < provider.rows()-1 {
				e.CursorRow++
			}

			rowLength := provider.rowLength(e.CursorRow)
			if e.CursorColumn > rowLength {
				e.CursorColumn = rowLength
			}
		})
	case fyne.KeyLeft:
		e.SetFields(func() {
			if e.CursorColumn > 0 {
				e.CursorColumn--
			} else if e.MultiLine && e.CursorRow > 0 {
				e.CursorRow--
				e.CursorColumn = provider.rowLength(e.CursorRow)
			}
		})
	case fyne.KeyRight:
		e.SetFields(func() {
			if e.MultiLine {
				rowLength := provider.rowLength(e.CursorRow)
				if e.CursorColumn < rowLength {
					e.CursorColumn++
				} else if e.CursorRow < provider.rows()-1 {
					e.CursorRow++
					e.CursorColumn = 0
				}
			} else if e.CursorColumn < provider.len() {
				e.CursorColumn++
			}
		})
	case fyne.KeyEnd:
		e.SetFields(func() {
			if e.MultiLine {
				e.CursorColumn = provider.rowLength(e.CursorRow)
			} else {
				e.CursorColumn = provider.len()
			}
		})
	case fyne.KeyHome:
		e.SetFields(func() {
			e.CursorColumn = 0
		})
	case fyne.KeyPageUp:
		e.SetFields(func() {
			if e.MultiLine {
				e.CursorRow = 0
			}
			e.CursorColumn = 0
		})
	case fyne.KeyPageDown:
		e.SetFields(func() {
			if e.MultiLine {
				e.CursorRow = provider.rows() - 1
				e.CursorColumn = provider.rowLength(e.CursorRow)
			} else {
				e.CursorColumn = provider.len()
			}
		})
	default:
		return
	}

	e.SetFields(func() {
		if e.CursorRow == e.selectRow && e.CursorColumn == e.selectColumn {
			e.selecting = false
		}
	})
	e.updateText(provider.String())
}

// TypedRune receives text input events when the Entry widget is focused.
func (e *Entry) TypedRune(r rune) {
	if e.Disabled() {
		return
	}

	var selecting bool
	e.SetFields(func() {
		if e.popUp != nil {
			e.popUp.Hide()
		}

		selecting = e.selecting
	})
	// if we've typed a character and we're selecting then replace the selection with the character
	if selecting {
		e.eraseSelection()
	}

	var content string
	e.SetFields(func() {
		provider := e.textProvider()
		e.selecting = false

		runes := []rune{r}
		pos := e.cursorTextPos()
		provider.insertAt(pos, runes)
		e.CursorRow, e.CursorColumn = e.rowColFromTextPos(pos + len(runes))

		content = provider.String()
	})
	e.updateText(content)
}

// TypedShortcut implements the Shortcutable interface
func (e *Entry) TypedShortcut(shortcut fyne.Shortcut) {
	e.shortcut.TypedShortcut(shortcut)
}

// concealed tells the rendering textProvider if we are a concealed field
func (e *Entry) concealed() bool {
	return e.Password
}

// copyToClipboard copies the current selection to a given clipboard.
// This does nothing if it is a concealed entry.
func (e *Entry) copyToClipboard(clipboard fyne.Clipboard) {
	if !e.selecting || e.concealed() {
		return
	}

	clipboard.SetContent(e.SelectedText())
}

func (e *Entry) cursorColAt(text []rune, pos fyne.Position) int {
	for i := 0; i < len(text); i++ {
		str := string(text[0 : i+1])
		wid := fyne.MeasureText(str, theme.TextSize(), e.textStyle()).Width + theme.Padding()
		if wid > pos.X {
			return i
		}
	}
	return len(text)
}

func (e *Entry) cursorTextPos() (pos int) {
	return e.textPosFromRowCol(e.CursorRow, e.CursorColumn)
}

// copyToClipboard copies the current selection to a given clipboard and then removes the selected text.
// This does nothing if it is a concealed entry.
func (e *Entry) cutToClipboard(clipboard fyne.Clipboard) {
	if !e.selecting || e.concealed() {
		return
	}

	e.copyToClipboard(clipboard)
	e.eraseSelection()
}

// eraseSelection removes the current selected region and moves the cursor
func (e *Entry) eraseSelection() {
	if e.Disabled() {
		return
	}

	provider := e.textProvider()
	posA, posB := e.selection()

	if posA == posB {
		return
	}

	e.SetFields(func() {
		provider.deleteFromTo(posA, posB)
		e.CursorRow, e.CursorColumn = e.rowColFromTextPos(posA)
		e.selectRow, e.selectColumn = e.CursorRow, e.CursorColumn
		e.selecting = false
	})
	e.updateText(provider.String())
}

// object returns the root object of the widget so it can be referenced
func (e *Entry) object() fyne.Widget {
	return nil
}

// pasteFromClipboard inserts text from the clipboard content,
// starting from the cursor position.
func (e *Entry) pasteFromClipboard(clipboard fyne.Clipboard) {
	if e.selecting {
		e.eraseSelection()
	}
	text := clipboard.Content()
	if !e.MultiLine {
		// format clipboard content to be compatible with single line entry
		text = strings.Replace(text, "\n", " ", -1)
	}
	provider := e.textProvider()
	runes := []rune(text)
	provider.insertAt(e.cursorTextPos(), runes)

	newlines := strings.Count(text, "\n")
	if newlines == 0 {
		e.CursorColumn += len(runes)
	} else {
		e.CursorRow += newlines
		lastNewlineIndex := 0
		for i, r := range runes {
			if r == '\n' {
				lastNewlineIndex = i
			}
		}
		e.CursorColumn = len(runes) - lastNewlineIndex - 1
	}
	e.updateText(provider.String())
	e.Refresh()
}

// placeholderProvider returns the placeholder text handler for this entry
func (e *Entry) placeholderProvider() *textProvider {
	if e.placeholder != nil {
		return e.placeholder
	}

	text := newTextProvider(e.PlaceHolder, &placeholderPresenter{e})
	text.ExtendBaseWidget(text)
	e.placeholder = text
	return e.placeholder
}

func (e *Entry) registerShortcut() {
	e.shortcut.AddShortcut(&fyne.ShortcutCut{}, func(se fyne.Shortcut) {
		cut := se.(*fyne.ShortcutCut)
		e.cutToClipboard(cut.Clipboard)
	})
	e.shortcut.AddShortcut(&fyne.ShortcutCopy{}, func(se fyne.Shortcut) {
		cpy := se.(*fyne.ShortcutCopy)
		e.copyToClipboard(cpy.Clipboard)
	})
	e.shortcut.AddShortcut(&fyne.ShortcutPaste{}, func(se fyne.Shortcut) {
		paste := se.(*fyne.ShortcutPaste)
		e.pasteFromClipboard(paste.Clipboard)
	})
	e.shortcut.AddShortcut(&fyne.ShortcutSelectAll{}, func(se fyne.Shortcut) {
		e.selectAll()
	})
}

// Obtains row,col from a given textual position
// expects a read or write lock to be held by the caller
func (e *Entry) rowColFromTextPos(pos int) (row int, col int) {
	provider := e.textProvider()
	for i := 0; i < provider.rows(); i++ {
		b := provider.rowBoundary(i)
		if b[0] <= pos {
			if b[1] < pos {
				row++
			}
			col = pos - b[0]
		} else {
			break
		}
	}
	return
}

// selectAll selects all text in entry
func (e *Entry) selectAll() {
	e.SetFieldsAndRefresh(func() {
		e.selectRow = 0
		e.selectColumn = 0

		lastRow := e.textProvider().rows() - 1
		e.CursorColumn = e.textProvider().rowLength(lastRow)
		e.CursorRow = lastRow
		e.selecting = true
	})
}

// selectingKeyHandler performs keypress action in the scenario that a selection
// is either a) in progress or b) about to start
// returns true if the keypress has been fully handled
func (e *Entry) selectingKeyHandler(key *fyne.KeyEvent) bool {

	if e.selectKeyDown && e.selecting == false {
		switch key.Name {
		case fyne.KeyUp, fyne.KeyDown,
			fyne.KeyLeft, fyne.KeyRight,
			fyne.KeyEnd, fyne.KeyHome,
			fyne.KeyPageUp, fyne.KeyPageDown:
			e.selecting = true
		}
	}

	if e.selecting == false {
		return false
	}

	switch key.Name {
	case fyne.KeyBackspace, fyne.KeyDelete:
		// clears the selection -- return handled
		e.eraseSelection()
		return true
	case fyne.KeyReturn, fyne.KeyEnter:
		// clear the selection -- return unhandled to add the newline
		e.eraseSelection()
		return false
	}

	if e.selectKeyDown == false {
		switch key.Name {
		case fyne.KeyLeft:
			// seek to the start of the selection -- return handled
			selectStart, _ := e.selection()
			e.SetFields(func() {
				e.CursorRow, e.CursorColumn = e.rowColFromTextPos(selectStart)
				e.selecting = false
			})
			return true
		case fyne.KeyRight:
			// seek to the end of the selection -- return handled
			_, selectEnd := e.selection()
			e.SetFields(func() {
				e.CursorRow, e.CursorColumn = e.rowColFromTextPos(selectEnd)
				e.selecting = false
			})
			return true
		case fyne.KeyUp, fyne.KeyDown, fyne.KeyEnd, fyne.KeyHome, fyne.KeyPageUp, fyne.KeyPageDown:
			// cursor movement without left or right shift -- clear selection and return unhandled
			e.selecting = false
			return false
		}
	}

	return false
}

// selection returns the start and end text positions for the selected span of text
// Note: this functionality depends on the relationship between the selection start row/col and
// the current cursor row/column.
// eg: (whitespace for clarity, '_' denotes cursor)
//   "T  e  s [t  i]_n  g" == 3, 5
//   "T  e  s_[t  i] n  g" == 3, 5
//   "T  e_[s  t  i] n  g" == 2, 5
func (e *Entry) selection() (start, end int) {
	noSelection := e.GetField(func() interface{} {
		return !e.selecting || (e.CursorRow == e.selectRow && e.CursorColumn == e.selectColumn)
	}).(bool)

	if noSelection {
		return -1, -1
	}

	e.SetFields(func() {
		// Find the selection start
		rowA, colA := e.CursorRow, e.CursorColumn
		rowB, colB := e.selectRow, e.selectColumn
		// Reposition if the cursors row is more than select start row, or if the row is the same and
		// the cursors col is more that the select start column
		if rowA > e.selectRow || (rowA == e.selectRow && colA > e.selectColumn) {
			rowA, colA = e.selectRow, e.selectColumn
			rowB, colB = e.CursorRow, e.CursorColumn
		}

		start, end = e.textPosFromRowCol(rowA, colA), e.textPosFromRowCol(rowB, colB)
	})
	return
}

// textAlign tells the rendering textProvider our alignment
func (e *Entry) textAlign() fyne.TextAlign {
	return fyne.TextAlignLeading
}

// textColor tells the rendering textProvider our color
func (e *Entry) textColor() color.Color {
	if e.Disabled() {
		return theme.DisabledTextColor()
	}
	return theme.TextColor()
}

// Obtains textual position from a given row and col
// expects a read or write lock to be held by the caller
func (e *Entry) textPosFromRowCol(row, col int) int {
	return e.textProvider().rowBoundary(row)[0] + col
}

// textProvider returns the text handler for this entry
func (e *Entry) textProvider() *textProvider {
	if e.text != nil {
		return e.text
	}

	text := newTextProvider(e.Text, e)
	text.ExtendBaseWidget(text)
	e.text = text
	return e.text
}

// textStyle tells the rendering textProvider our style
func (e *Entry) textStyle() fyne.TextStyle {
	return fyne.TextStyle{}
}

// textWrap tells the rendering textProvider our wrapping
func (e *Entry) textWrap() fyne.TextWrap {
	if e.Wrapping == fyne.TextTruncate {
		fyne.LogError("Entry does not allow Truncation", nil)
		e.Wrapping = fyne.TextWrapOff
	}
	if !e.MultiLine && e.Wrapping != fyne.TextWrapOff {
		fyne.LogError("Entry cannot wrap single line", nil)
		e.Wrapping = fyne.TextWrapOff
	}
	return e.Wrapping
}

func (e *Entry) updateMousePointer(ev *fyne.PointEvent, rightClick bool) {
	if !e.Focused() && !e.Disabled() {
		e.FocusGained()
	}

	e.SetFieldsAndRefresh(func() {

		rowHeight := e.textProvider().charMinSize().Height
		row := int(math.Floor(float64(ev.Position.Y-theme.Padding()) / float64(rowHeight)))
		col := 0
		if row < 0 {
			row = 0
		} else if row >= e.textProvider().rows() {
			row = e.textProvider().rows() - 1
			col = 0
		} else {
			col = e.cursorColAt(e.textProvider().row(row), ev.Position)
		}

		if !rightClick || rightClick && !e.selecting {
			e.CursorRow = row
			e.CursorColumn = col
		}

		if !e.selecting {
			e.selectRow = row
			e.selectColumn = col
		}
	})
}

// updateText updates the internal text to the given value
func (e *Entry) updateText(text string) {
	e.SetFieldsAndRefresh(func() {
		changed := e.Text != text
		e.Text = text

		if changed && e.OnChanged != nil {
			e.OnChanged(text)
		}
	})
}

var _ fyne.WidgetRenderer = (*entryRenderer)(nil)

type entryRenderer struct {
	line, cursor *canvas.Rectangle
	selection    []fyne.CanvasObject

	objects []fyne.CanvasObject
	entry   *Entry
}

// BackgroundColor satisfies the fyne.WidgetRenderer interface.
func (r *entryRenderer) BackgroundColor() color.Color {
	return theme.BackgroundColor()
}

// Destroy satisfies the fyne.WidgetRenderer interface.
func (r *entryRenderer) Destroy() {
}

// Layout the components of the entry widget.
func (r *entryRenderer) Layout(size fyne.Size) {
	r.line.Resize(fyne.NewSize(size.Width, theme.Padding()))
	r.line.Move(fyne.NewPos(0, size.Height-theme.Padding()))

	actionIconSize := fyne.NewSize(0, 0)
	if r.entry.ActionItem != nil {
		actionIconSize = fyne.NewSize(theme.IconInlineSize(), theme.IconInlineSize())
		r.entry.ActionItem.Resize(actionIconSize)
		r.entry.ActionItem.Move(fyne.NewPos(size.Width-actionIconSize.Width-2*theme.Padding(), theme.Padding()*2))
	}

	entrySize := size.Subtract(fyne.NewSize(theme.Padding()*2-actionIconSize.Width, theme.Padding()*2))
	entryPos := fyne.NewPos(theme.Padding(), theme.Padding())
	r.entry.text.Resize(entrySize)
	r.entry.text.Move(entryPos)
	r.entry.placeholder.Resize(entrySize)
	r.entry.placeholder.Move(entryPos)
}

// MinSize calculates the minimum size of an entry widget.
// This is based on the contained text with a standard amount of padding added.
// If MultiLine is true then we will reserve space for at leasts 3 lines
func (r *entryRenderer) MinSize() fyne.Size {
	minSize := r.entry.placeholderProvider().MinSize()

	if r.entry.textProvider().len() > 0 {
		minSize = r.entry.text.MinSize()
	}

	if r.entry.MultiLine == true {
		// ensure multiline height is at least charMinSize * multilineRows
		minSize.Height = fyne.Max(minSize.Height, r.entry.text.charMinSize().Height*multiLineRows)
	}

	return minSize.Add(fyne.NewSize(theme.Padding()*4, theme.Padding()*2))
}

// Objects satisfies the fyne.WidgetRenderer interface.
func (r *entryRenderer) Objects() []fyne.CanvasObject {
	// Objects are generated dynamically force selection rectangles to appear underneath the text
	if r.entry.selecting {
		return append(r.selection, r.objects...)
	}
	return r.objects
}

// Refresh satisfies the fyne.WidgetRenderer interface.
func (r *entryRenderer) Refresh() {
	if r.entry.Text != string(r.entry.textProvider().buffer) {
		r.entry.textProvider().SetText(r.entry.Text)
	}
	if r.entry.textProvider().len() == 0 && r.entry.Visible() {
		r.entry.placeholderProvider().Show()
	} else if r.entry.placeholderProvider().Visible() {
		r.entry.placeholderProvider().Hide()
	}

	r.cursor.FillColor = theme.FocusColor()
	if r.entry.focused {
		r.cursor.Show()
		r.line.FillColor = theme.FocusColor()
	} else {
		r.cursor.Hide()
		if r.entry.Disabled() {
			r.line.FillColor = theme.DisabledButtonColor()
		} else {
			r.line.FillColor = theme.ButtonColor()
		}
	}
	r.moveCursor()

	for _, selection := range r.selection {
		selection.(*canvas.Rectangle).Hidden = !r.entry.focused && !r.entry.disabled
		selection.(*canvas.Rectangle).FillColor = theme.FocusColor()
	}

	r.entry.text.SetFields(func() {
		r.entry.text.updateRowBounds()
	})
	r.entry.placeholder.SetFields(func() {
		r.entry.placeholder.updateRowBounds()
	})
	r.entry.text.Refresh()
	r.entry.placeholder.Refresh()
	if r.entry.ActionItem != nil {
		r.entry.ActionItem.Refresh()
	}
	canvas.Refresh(r.entry.super())
}

// This process builds a slice of rectangles:
// - one entry per row of text
// - ordered by row order as they occur in multiline text
// This process could be optimized in the scenario where the user is selecting upwards:
// If the upwards case instead produces an order-reversed slice then only the newest rectangle would
// require movement and resizing. The existing solution creates a new rectangle and then moves/resizes
// all rectangles to comply with the occurrence order as stated above.
func (r *entryRenderer) buildSelection() {
	r.entry.propertyLock.RLock()
	cursorRow, cursorCol := r.entry.CursorRow, r.entry.CursorColumn
	selectRow, selectCol := -1, -1
	if r.entry.selecting {
		selectRow = r.entry.selectRow
		selectCol = r.entry.selectColumn
	}
	r.entry.propertyLock.RUnlock()

	if selectRow == -1 {
		r.selection = r.selection[:0]

		return
	}

	provider := r.entry.textProvider()
	// Convert column, row into x,y
	getCoordinates := func(column int, row int) (int, int) {
		sz := provider.lineSizeToColumn(column, row)
		return sz.Width + theme.Padding()*2, sz.Height*row + theme.Padding()*2
	}

	lineHeight := r.entry.text.charMinSize().Height

	minmax := func(a, b int) (int, int) {
		if a < b {
			return a, b
		}
		return b, a
	}

	// The remainder of the function calculates the set of boxes and add them to r.selection

	selectStartRow, selectEndRow := minmax(selectRow, cursorRow)
	selectStartCol, selectEndCol := minmax(selectCol, cursorCol)
	if selectRow < cursorRow {
		selectStartCol, selectEndCol = selectCol, cursorCol
	}
	if selectRow > cursorRow {
		selectStartCol, selectEndCol = cursorCol, selectCol
	}
	rowCount := selectEndRow - selectStartRow + 1

	// trim r.selection to remove unwanted old rectangles
	if len(r.selection) > rowCount {
		r.selection = r.selection[:rowCount]
	}

	// build a rectangle for each row and add it to r.selection
	for i := 0; i < rowCount; i++ {
		if len(r.selection) <= i {
			box := canvas.NewRectangle(theme.FocusColor())
			r.selection = append(r.selection, box)
		}

		// determine starting/ending columns for this rectangle
		row := selectStartRow + i
		startCol, endCol := selectStartCol, selectEndCol
		if selectStartRow < row {
			startCol = 0
		}
		if selectEndRow > row {
			endCol = provider.rowLength(row)
		}

		// translate columns and row into draw coordinates
		x1, y1 := getCoordinates(startCol, row)
		x2, _ := getCoordinates(endCol, row)

		// resize and reposition each rectangle
		r.selection[i].Resize(fyne.NewSize(x2-x1+1, lineHeight))
		r.selection[i].Move(fyne.NewPos(x1-1, y1))
	}
}

func (r *entryRenderer) moveCursor() {
	// build r.selection[] if the user has made a selection
	r.buildSelection()
	provider := r.entry.GetField(func() interface{} {
		return r.entry.textProvider()
	}).(*textProvider)

	var xPos, yPos int
	provider.ReadFields(func() {
		size := provider.lineSizeToColumn(r.entry.CursorColumn, r.entry.CursorRow)
		xPos = size.Width
		yPos = size.Height * r.entry.CursorRow
	})

	r.entry.SetFields(func() {
		lineHeight := r.entry.text.charMinSize().Height
		r.cursor.Resize(fyne.NewSize(2, lineHeight))
		r.cursor.Move(fyne.NewPos(xPos-1+theme.Padding()*2, yPos+theme.Padding()*2))

		if r.entry.OnCursorChanged != nil {
			r.entry.OnCursorChanged()
		}
	})
}

var _ desktop.Cursorable = (*passwordRevealer)(nil)
var _ fyne.Tappable = (*passwordRevealer)(nil)
var _ fyne.Widget = (*passwordRevealer)(nil)

type passwordRevealer struct {
	BaseWidget

	icon  *canvas.Image
	entry *Entry
}

func newPasswordRevealer(e *Entry) *passwordRevealer {
	pr := &passwordRevealer{
		icon:  canvas.NewImageFromResource(theme.VisibilityOffIcon()),
		entry: e,
	}
	pr.ExtendBaseWidget(pr)
	return pr
}

// CreateRenderer satisfies the fyne.Widget interface.
func (r *passwordRevealer) CreateRenderer() fyne.WidgetRenderer {
	return &passwordRevealerRenderer{
		BaseRenderer: widget.NewBaseRenderer([]fyne.CanvasObject{r.icon}),
		icon:         r.icon,
		entry:        r.entry,
	}
}

// Cursor satisfies the desktop.Cursorable interface.
func (r *passwordRevealer) Cursor() desktop.Cursor {
	return desktop.DefaultCursor
}

// Tapped satisfies the fyne.Tappable interface.
func (r *passwordRevealer) Tapped(*fyne.PointEvent) {
	r.entry.SetFieldsAndRefresh(func() {
		r.entry.Password = !r.entry.Password
	})
	fyne.CurrentApp().Driver().CanvasForObject(r).Focus(r.entry)
}

var _ fyne.WidgetRenderer = (*passwordRevealerRenderer)(nil)

type passwordRevealerRenderer struct {
	widget.BaseRenderer
	entry *Entry
	icon  *canvas.Image
}

// Layout satisfies the fyne.WidgetRenderer interface.
func (r *passwordRevealerRenderer) Layout(size fyne.Size) {
	r.icon.Resize(fyne.NewSize(theme.IconInlineSize(), theme.IconInlineSize()))
	r.icon.Move(fyne.NewPos((size.Width-theme.IconInlineSize())/2, (size.Height-theme.IconInlineSize())/2))
}

// MinSize satisfies the fyne.WidgetRenderer interface.
func (r *passwordRevealerRenderer) MinSize() fyne.Size {
	return fyne.NewSize(theme.IconInlineSize(), theme.IconInlineSize())
}

// Refresh satisfies the fyne.WidgetRenderer interface.
func (r *passwordRevealerRenderer) Refresh() {
	if r.entry.GetField(func() interface{} {
		return !r.entry.Password
	}).(bool) {
		r.icon.Resource = theme.VisibilityIcon()
	} else {
		r.icon.Resource = theme.VisibilityOffIcon()
	}
	canvas.Refresh(r.icon)
}

type placeholderPresenter struct {
	e *Entry
}

// concealed tells the rendering textProvider if we are a concealed field
// placeholder text is not obfuscated, returning false
func (p *placeholderPresenter) concealed() bool {
	return false
}

// object returns the root object of the widget so it can be referenced
func (p *placeholderPresenter) object() fyne.Widget {
	return nil
}

// textAlign tells the rendering textProvider our alignment
func (p *placeholderPresenter) textAlign() fyne.TextAlign {
	return fyne.TextAlignLeading
}

// textColor tells the rendering textProvider our color
func (p *placeholderPresenter) textColor() color.Color {
	return theme.PlaceHolderColor()
}

// textStyle tells the rendering textProvider our style
func (p *placeholderPresenter) textStyle() fyne.TextStyle {
	return fyne.TextStyle{}
}

// textWrap tells the rendering textProvider our wrapping
func (p *placeholderPresenter) textWrap() fyne.TextWrap {
	return p.e.Wrapping
}

// getTextWhitespaceRegion returns the start/end markers for selection highlight on starting from col
// and expanding to the start and end of the whitespace or text underneath the specified position.
func getTextWhitespaceRegion(row []rune, col int) (int, int) {

	if len(row) == 0 || col < 0 {
		return -1, -1
	}

	// If the click position exceeds the length of text then snap it to the end
	if col >= len(row) {
		col = len(row) - 1
	}

	// maps: " fi-sh 日本語本語日  \t "
	// into: " -- -- ------   "
	space := func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		// If this rune is a typical word separator then classify it as whitespace
		if strings.ContainsRune(doubleClickWordSeperator, r) {
			return ' '
		}
		return '-'
	}
	toks := strings.Map(space, string(row))

	c := byte(' ')
	if toks[col] == ' ' {
		c = byte('-')
	}

	// LastIndexByte + 1 ensures that the position of the unwanted character 'c' is excluded
	// +1 also has the added side effect whereby if 'c' isn't found then -1 is snapped to 0
	start := strings.LastIndexByte(toks[:col], c) + 1

	// IndexByte will find the position of the next unwanted character, this is to be the end
	// marker for the selection
	end := strings.IndexByte(toks[col:], c)

	if end == -1 {
		end = len(toks) // snap end to len(toks) if it results in -1
	} else {
		end += col // otherwise include the text slice position
	}
	return start, end
}
