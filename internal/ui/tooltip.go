package ui

import (
	"fyne.io/fyne/v2"
	fynetooltip "github.com/dweymouth/fyne-tooltip"
	ttwidget "github.com/dweymouth/fyne-tooltip/widget"
)

// WrapWithTooltip wraps a widget with tooltip functionality using fyne-tooltip
func WrapWithTooltip(w fyne.CanvasObject, tooltipText string) fyne.CanvasObject {
	// Check if the widget is already a tooltip-enabled widget
	if ttw, ok := w.(interface{ SetToolTip(string) }); ok {
		ttw.SetToolTip(tooltipText)
		return w
	}

	// For non-tooltip widgets, we need to create a tooltip-enabled wrapper
	// This is a simplified approach - for full functionality, you'd need to
	// implement proper widget wrapping as shown in the fyne-tooltip examples

	// For now, return the original widget
	// TODO: Implement proper widget wrapping for non-tooltip widgets
	return w
}

// CreateTooltipButton creates a button with tooltip support
func CreateTooltipButton(text, tooltip string, tapped func()) *ttwidget.Button {
	btn := ttwidget.NewButton(text, tapped)
	btn.SetToolTip(tooltip)
	return btn
}

// CreateTooltipButtonWithIcon creates a button with icon and tooltip support
func CreateTooltipButtonWithIcon(text, tooltip string, icon fyne.Resource, tapped func()) *ttwidget.Button {
	btn := ttwidget.NewButtonWithIcon(text, icon, tapped)
	btn.SetToolTip(tooltip)
	return btn
}

// AddTooltipLayer adds the tooltip layer to a window's content
func AddTooltipLayer(content fyne.CanvasObject, canvas fyne.Canvas) fyne.CanvasObject {
	return fynetooltip.AddWindowToolTipLayer(content, canvas)
}
