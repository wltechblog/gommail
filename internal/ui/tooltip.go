package ui

import (
	"fyne.io/fyne/v2"
	fynetooltip "github.com/dweymouth/fyne-tooltip"
	ttwidget "github.com/dweymouth/fyne-tooltip/widget"
)

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
